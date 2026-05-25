package adapter

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/auth"
	legacyadmin "github.com/wjsoj/CPA-Claude/internal/admin"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/saas/admin"
	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/tokens"
)

// Mount attaches all /api/v2/* SaaS routes onto engine. Public-routes (auth
// + billing notify) sit outside RequireUser. credH may be nil — when set,
// /api/v2/admin/credentials/* is exposed. legacyH may be nil — when set, the
// /api/v2/admin/* group also exposes request-log queries + Anthropic OAuth
// quota probe (handlers reused from the legacy /mgmt-console panel).
func Mount(engine *gin.Engine, store *db.DB, authH *saasauth.Handler, tokensH *tokens.Handler, billingH *billing.Handler, adminH *admin.Handler, credH *admin.CredHandler, iss *saasauth.Issuer, legacyH *legacyadmin.Handler, logDir string, catalog *pricing.Catalog) {
	v2 := engine.Group("/api/v2")

	// Public.
	v2.GET("/site", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	v2.GET("/exchange-rate", billingH.UserRateRouteShim())
	authG := v2.Group("/auth")
	authH.Routes(authG)
	billingH.PublicRoutes(v2)

	// Authenticated.
	authed := v2.Group("")
	authed.Use(saasauth.RequireUser(iss, store))
	authed.GET("/me", func(c *gin.Context) {
		u := saasauth.CurrentUser(c)
		if u == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		g, _ := store.GetGroup(c.Request.Context(), u.GroupID)
		c.JSON(http.StatusOK, gin.H{
			"user": gin.H{
				"id": u.ID, "email": u.Email, "role": u.Role,
				"balance_usd": u.BalanceUSD, "group_id": u.GroupID,
				"email_verified": u.EmailVerified, "created_at": u.CreatedAt.Unix(),
			},
			"group": g,
		})
	})
	tokensH.Routes(authed.Group("/tokens"))
	billingH.UserRoutes(authed.Group("/billing"))

	// Available credential channels — the dropdown source for the per-token
	// "渠道" selector. Deduplicated by group name; each entry reports which
	// provider(s) back it and how many usable credentials it currently has.
	// "Usable" = not Disabled and not HardFailure — credentials in cooldown
	// (quota / rate-limit) still count, since they recover automatically.
	// The empty-string group (public pool) is exposed as "default".
	authed.GET("/channels", func(c *gin.Context) {
		if credH == nil || credH.Pool == nil {
			c.JSON(http.StatusOK, gin.H{"channels": []any{}})
			return
		}
		type chanInfo struct {
			Name      string   `json:"name"`
			Providers []string `json:"providers"`
			Count     int      `json:"count"`
		}
		acc := map[string]*chanInfo{}
		provSeen := map[string]map[string]bool{}
		for _, s := range credH.Pool.Status() {
			a := s.Auth
			if a.Disabled {
				continue
			}
			if live := credH.Pool.FindByID(a.ID); live != nil {
				if _, hardFail, _, _ := live.HealthSnapshot(); hardFail {
					continue
				}
			}
			name := a.Group
			if name == "" {
				name = "default"
			}
			ci, ok := acc[name]
			if !ok {
				ci = &chanInfo{Name: name}
				acc[name] = ci
				provSeen[name] = map[string]bool{}
			}
			ci.Count++
			prov := string(a.Provider)
			if prov != "" && !provSeen[name][prov] {
				provSeen[name][prov] = true
				ci.Providers = append(ci.Providers, prov)
			}
		}
		out := make([]*chanInfo, 0, len(acc))
		for _, ci := range acc {
			sort.Strings(ci.Providers)
			out = append(out, ci)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Name == "default" {
				return false
			}
			if out[j].Name == "default" {
				return true
			}
			return out[i].Name < out[j].Name
		})
		c.JSON(http.StatusOK, gin.H{"channels": out})
	})

	// Per-user request log. Same shape as /admin/api/requests but filtered
	// to records emitted while the requester was the authenticated user —
	// powers the /app/logs page where customers reconcile every charge
	// against the catalog rate. Read-only, no mutation surface.
	authed.GET("/me/requests", func(c *gin.Context) {
		u := saasauth.CurrentUser(c)
		if u == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		f := requestlog.Filter{
			Dir:    logDir,
			UserID: u.ID,
			Limit:  limit,
			Offset: offset,
		}
		// Optional secondary filters — useful for users wanting to drill
		// down to a single token or model.
		if mtok := c.Query("model"); mtok != "" {
			f.Model = mtok
		}
		if prov := c.Query("provider"); prov != "" {
			f.Provider = prov
		}
		if ct := c.Query("client_token"); ct != "" {
			f.ClientToken = ct
		}
		res, err := requestlog.Query(f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Attach the price card for every distinct (provider, model) that
		// appears so the frontend can render a per-row "how was this
		// charged" breakdown without a follow-up RPC. Keys are canonical
		// "<provider>/<model>" matching catalog.Lookup so the frontend can
		// resolve via the shared lookupPrice helper — keying by bare model
		// would silently fall back to the global default for OpenAI rows
		// (since the catalog stores them under "openai/...").
		prices := map[string]gin.H{}
		seen := map[string]struct{}{}
		add := func(provider, model string) {
			if model == "" {
				return
			}
			prov := provider
			if prov == "" {
				prov = auth.ProviderAnthropic // legacy records pre-dating the field
			}
			key := prov + "/" + model
			if _, ok := seen[key]; ok {
				return
			}
			seen[key] = struct{}{}
			p := catalog.Lookup(prov, model)
			prices[key] = gin.H{
				"input_per_1m":        p.InputPer1M,
				"output_per_1m":       p.OutputPer1M,
				"cache_read_per_1m":   p.CacheReadPer1M,
				"cache_create_per_1m": p.CacheCreatePer1M,
			}
		}
		for _, e := range res.Entries {
			add(e.Provider, e.Model)
		}
		c.JSON(http.StatusOK, gin.H{
			"summary":  res.Summary,
			"by_model": res.ByModel,
			"entries":  res.Entries,
			"scanned":  res.Scanned,
			"pricing":  prices,
		})
	})

	// Public groups (read-only, used on landing/pricing pages).
	v2.GET("/groups", func(c *gin.Context) {
		gs, err := store.ListGroups(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"groups": gs})
	})

	// Public health snapshot for /status. Two row kinds:
	//   - OAuth: aggregated to one row per (provider, model). Multiple OAuth
	//     credentials backing the same model are an implementation detail;
	//     the public page just needs "can the model be served?".
	//   - API key: one row per credential, like the operator panel. API keys
	//     are usually pinned to specific upstream gateways (tcdmx / fucheers
	//     / etc.) which fail independently, so we keep them split out.
	// Rows whose auth_id is no longer in the live pool are dropped — they
	// belong to deleted credentials.
	v2.GET("/health", func(c *gin.Context) {
		hs, err := store.ListModelHealth(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Build live-pool maps from credH.Pool. When credH is nil (unwired),
		// fall through with no pool filtering — every row is treated as live.
		liveKind := map[string]auth.Kind{}
		havePool := credH != nil && credH.Pool != nil
		if havePool {
			for _, st := range credH.Pool.Status() {
				liveKind[st.Auth.ID] = st.Auth.Kind
			}
		}

		// Partition: OAuth rows go to aggregation groups, API-key rows go
		// straight to the output. Rows missing from the live pool (deleted
		// creds) are skipped.
		type groupKey struct{ provider, model string }
		oauthGroups := map[groupKey][]*db.ModelHealth{}
		apikeyRows := make([]*db.ModelHealth, 0)
		for _, rec := range hs {
			if havePool {
				k, ok := liveKind[rec.AuthID]
				if !ok {
					continue
				}
				if k == auth.KindOAuth {
					oauthGroups[groupKey{provider: rec.Provider, model: rec.Model}] = append(
						oauthGroups[groupKey{provider: rec.Provider, model: rec.Model}], rec,
					)
				} else {
					apikeyRows = append(apikeyRows, rec)
				}
			} else {
				// Best-effort fallback: treat unknown rows as their own row
				// (no aggregation), so something still renders.
				apikeyRows = append(apikeyRows, rec)
			}
		}

		// Stable display-name counter for API-key rows (same scheme as the
		// operator panel and the previous public endpoint).
		counters := map[string]int{}
		apikeyName := func(provider string) string {
			prov := "claude"
			if provider == auth.ProviderOpenAI {
				prov = "codex"
			}
			counters[prov]++
			return fmt.Sprintf("%s-api-%03d", prov, counters[prov])
		}

		// Stable order: Anthropic before OpenAI, OAuth aggregates before
		// API-key singletons within each provider, then by model name.
		oauthKeys := make([]groupKey, 0, len(oauthGroups))
		for k := range oauthGroups {
			oauthKeys = append(oauthKeys, k)
		}
		sort.Slice(oauthKeys, func(i, j int) bool {
			if oauthKeys[i].provider != oauthKeys[j].provider {
				return oauthKeys[i].provider < oauthKeys[j].provider
			}
			return oauthKeys[i].model < oauthKeys[j].model
		})
		sort.SliceStable(apikeyRows, func(i, j int) bool {
			if apikeyRows[i].Provider != apikeyRows[j].Provider {
				return apikeyRows[i].Provider < apikeyRows[j].Provider
			}
			if apikeyRows[i].Model != apikeyRows[j].Model {
				return apikeyRows[i].Model < apikeyRows[j].Model
			}
			return apikeyRows[i].AuthID < apikeyRows[j].AuthID
		})

		const bucketSec = 300 // 5-minute buckets for merged OAuth history

		out := make([]gin.H, 0, len(oauthGroups)+len(apikeyRows))
		nextID := 1

		// OAuth aggregates.
		for _, k := range oauthKeys {
			recs := oauthGroups[k]
			anyOK := false
			latSum, latN := 0, 0
			var newestChecked int64
			for _, r := range recs {
				if r.Status == "ok" {
					anyOK = true
					if r.LatencyMs > 0 {
						latSum += r.LatencyMs
						latN++
					}
				}
				if r.CheckedAt.Unix() > newestChecked {
					newestChecked = r.CheckedAt.Unix()
				}
			}
			status := "fail"
			if anyOK {
				status = "ok"
			}
			meanLat := 0
			if latN > 0 {
				meanLat = latSum / latN
			}

			type bucket struct {
				okCount, failCount int
				latSum, latN       int
				ts                 int64
			}
			buckets := map[int64]*bucket{}
			for _, r := range recs {
				hist, _ := store.ListModelHealthHistory(c.Request.Context(), r.AuthID, r.Model, 90)
				for _, h := range hist {
					ts := h.CheckedAt.Unix()
					bkey := ts / bucketSec
					b := buckets[bkey]
					if b == nil {
						b = &bucket{ts: ts}
						buckets[bkey] = b
					}
					if ts > b.ts {
						b.ts = ts
					}
					if h.Status == "ok" {
						b.okCount++
						if h.LatencyMs > 0 {
							b.latSum += h.LatencyMs
							b.latN++
						}
					} else {
						b.failCount++
					}
				}
			}
			bkeys := make([]int64, 0, len(buckets))
			for bk := range buckets {
				bkeys = append(bkeys, bk)
			}
			sort.Slice(bkeys, func(i, j int) bool { return bkeys[i] < bkeys[j] })
			if len(bkeys) > 90 {
				bkeys = bkeys[len(bkeys)-90:]
			}
			histSlice := make([]gin.H, 0, len(bkeys))
			for _, bk := range bkeys {
				b := buckets[bk]
				bSt := "fail"
				if b.okCount > 0 {
					bSt = "ok"
				}
				bLat := 0
				if b.latN > 0 {
					bLat = b.latSum / b.latN
				}
				histSlice = append(histSlice, gin.H{
					"status":     bSt,
					"latency_ms": bLat,
					"checked_at": b.ts,
				})
			}

			out = append(out, gin.H{
				"id":           nextID,
				"display_name": k.model,
				"provider":     k.provider,
				"model":        k.model,
				"kind":         "oauth",
				"status":       status,
				"latency_ms":   meanLat,
				"checked_at":   newestChecked,
				"history":      histSlice,
				"oauth_count":  len(recs),
			})
			nextID++
		}

		// API-key singletons.
		for _, rec := range apikeyRows {
			hist, _ := store.ListModelHealthHistory(c.Request.Context(), rec.AuthID, rec.Model, 90)
			histSlice := make([]gin.H, 0, len(hist))
			for _, r := range hist {
				histSlice = append(histSlice, gin.H{
					"status":     r.Status,
					"latency_ms": r.LatencyMs,
					"checked_at": r.CheckedAt.Unix(),
				})
			}
			out = append(out, gin.H{
				"id":           nextID,
				"display_name": apikeyName(rec.Provider),
				"provider":     rec.Provider,
				"model":        rec.Model,
				"kind":         "apikey",
				"status":       rec.Status,
				"latency_ms":   rec.LatencyMs,
				"checked_at":   rec.CheckedAt.Unix(),
				"history":      histSlice,
				// `error` intentionally omitted — operator-only detail.
			})
			nextID++
		}

		c.JSON(http.StatusOK, gin.H{"checks": out, "as_of": time.Now().Unix()})
	})

	// Fleet-wide wallet aggregate is exposed to any signed-in user — it
	// powers the "Saved by us" tile on the operator console which itself
	// is open to all users (per the SSO design). No PII, just sums.
	authed.GET("/admin/wallet-totals", adminH.WalletTotalsHandler())

	// Admin (operator-only).
	adminG := authed.Group("/admin")
	adminG.Use(saasauth.RequireAdmin())
	adminH.Routes(adminG)
	if credH != nil {
		credH.Routes(adminG)
	}
	if legacyH != nil {
		legacyH.RegisterSaaSBridge(adminG)
	}
}
