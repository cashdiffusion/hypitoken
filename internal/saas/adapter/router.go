package adapter

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	legacyadmin "github.com/wjsoj/CPA-Claude/internal/admin"
	"github.com/wjsoj/CPA-Claude/internal/saas/admin"
	"github.com/wjsoj/CPA-Claude/internal/saas/analytics"
	"github.com/wjsoj/CPA-Claude/internal/saas/arena"
	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/growth"
	"github.com/wjsoj/CPA-Claude/internal/saas/profile"
	"github.com/wjsoj/CPA-Claude/internal/saas/tokens"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/requestlog"
)

// Mount attaches all /api/v2/* SaaS routes onto engine. Public-routes (auth
// + billing notify) sit outside RequireUser. credH may be nil — when set,
// /api/v2/admin/credentials/* is exposed. legacyH may be nil — when set, the
// /api/v2/admin/* group also exposes request-log queries + Anthropic OAuth
// quota probe (handlers reused from the legacy operator API).
func Mount(engine *gin.Engine, store *db.DB, authH *saasauth.Handler, tokensH *tokens.Handler, billingH *billing.Handler, adminH *admin.Handler, credH *admin.CredHandler, iss *saasauth.Issuer, legacyH *legacyadmin.Handler, logDir string, catalog *pricing.Catalog, growthH *growth.Service, analyticsH *analytics.Service, arenaH *arena.Service, profileH *profile.Handler) {
	v2 := engine.Group("/api/v2")

	// Public.
	v2.GET("/site", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	v2.GET("/exchange-rate", billingH.UserRateRouteShim())
	authG := v2.Group("/auth")
	authH.Routes(authG)
	billingH.PublicRoutes(v2)

	// Growth (marketing attribution) — public, unauthenticated visit/dwell
	// tracking beacons. nil when the module is disabled.
	if growthH != nil {
		growthH.PublicRoutes(v2)
	}
	// Analytics (site-wide visitor behaviour) — public, unauthenticated
	// pageview/action/dwell beacons. nil when the module is disabled.
	if analyticsH != nil {
		analyticsH.PublicRoutes(v2)
	}

	// Arena SSE office stream — registered on the public group because it does
	// its own JWT auth (EventSource can't send an Authorization header, so the
	// token rides the ?access_token= query parameter). The leaderboard itself
	// is a normal authed GET, registered below.
	if arenaH != nil {
		arenaH.PublicRoutes(v2)
	}

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
		user := gin.H{
			"id": u.ID, "email": u.Email, "role": u.Role,
			"balance_usd": u.BalanceUSD, "group_id": u.GroupID,
			"email_verified": u.EmailVerified, "created_at": u.CreatedAt.Unix(),
		}
		// Attach the public-arena profile (nickname + opt-in) so the dashboard
		// can greet the user by name without a second round-trip. Lazily created.
		if p, perr := store.GetOrCreateProfile(c.Request.Context(), u.ID); perr == nil {
			user["display_name"] = p.DisplayName
			user["name_is_default"] = p.NameIsDefault
			user["public_opt_in"] = p.PublicOptIn
		}
		c.JSON(http.StatusOK, gin.H{"user": user, "group": g})
	})
	tokensH.Routes(authed.Group("/tokens"))
	billingH.UserRoutes(authed.Group("/billing"))
	// Arena leaderboard + profile (nickname / public opt-in / IP greeting) —
	// all authed user routes.
	if arenaH != nil {
		arenaH.AuthedRoutes(authed)
	}
	if profileH != nil {
		profileH.Routes(authed)
	}

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
			prov := a.Provider
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

	// Per-user console summary — account-level aggregates powering the
	// /app/console "个人" tab. Mirrors the platform KPI shape (requests /
	// tokens-in / tokens-out / total) but scoped to this user's own request
	// log via the UserID filter. `total` is all-time; `today` is the current
	// UTC day's bucket from ByDay (zero Aggregate when the user has no traffic
	// today). Same redaction posture as /me/requests — a user only ever sees
	// their own rows, never anyone else's or any fleet/credential detail.
	authed.GET("/me/console", func(c *gin.Context) {
		u := saasauth.CurrentUser(c)
		if u == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		// Limit: 1 — we only need the aggregates (Summary/ByDay/ByModel), not
		// the entry page; the maps are computed over every matched record
		// regardless of Limit.
		res, err := requestlog.Query(requestlog.Filter{Dir: logDir, UserID: u.ID, Limit: 1})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		nowUTC := time.Now().UTC()
		today := nowUTC.Format("2006-01-02")
		// Actual spend comes from the wallet ledger (charge rows = official ×
		// pricing-group multiplier), NOT requestlog.CostUSD, which records the
		// *official* price before the discount. spent_total/spent_today are the
		// figures the user truly paid, so the dashboard "累计消费" and this tab
		// agree.
		spentTotal, _ := store.SumChargeSince(c.Request.Context(), u.ID, time.Time{})
		dayStart := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
		spentToday, _ := store.SumChargeSince(c.Request.Context(), u.ID, dayStart)
		c.JSON(http.StatusOK, gin.H{
			"total":       res.Summary,
			"today":       res.ByDay[today],
			"today_key":   today,
			"by_model":    res.ByModel,
			"by_day":      res.ByDay,
			"balance_usd": u.BalanceUSD,
			"spent_total": spentTotal,
			"spent_today": spentToday,
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

	// Public per-provider availability monitor. Aggregates ALL credentials of a
	// provider into a single line (no oauth/api or per-model split), exposing
	// two uptime strips: a fine-grained recent timeline (10-minute slots over
	// the last 24h) and a daily rollup (last 14 days). This is the status-page
	// data source; it answers "is Claude / Codex usable right now and how has
	// availability looked" without leaking per-credential detail.
	v2.GET("/health/monitor", func(c *gin.Context) {
		const (
			recentSlots = 144 // 24h / 10min
			recentSlotS = int64(600)
			dailyDays   = 30
			daySec      = int64(86400)
		)
		now := time.Now()
		nowU := now.Unix()
		recentStart := nowU - int64(recentSlots)*recentSlotS

		// Current status per provider from the live model_health rows.
		curr, _ := store.ListModelHealth(c.Request.Context())
		type pcount struct {
			ok, total int
			checkedAt int64
		}
		cur := map[string]*pcount{}
		for _, r := range curr {
			p := cur[r.Provider]
			if p == nil {
				p = &pcount{}
				cur[r.Provider] = p
			}
			p.total++
			if r.Status == "ok" {
				p.ok++
			}
			if r.CheckedAt.Unix() > p.checkedAt {
				p.checkedAt = r.CheckedAt.Unix()
			}
		}

		providers := []struct{ key, name string }{
			{auth.ProviderAnthropic, "Claude"},
			{auth.ProviderOpenAI, "Codex"},
		}
		out := make([]gin.H, 0, len(providers))
		for _, pv := range providers {
			cc := cur[pv.key]
			if cc == nil || cc.total == 0 {
				continue // no credentials for this provider — omit the card
			}
			// The pill answers one question: can a new user use the service right
			// now? Yes as long as AT LEAST ONE credential is healthy — the pool
			// routes around the dead ones. It is NOT a credential-health ratio:
			// many creds may sit dead/quota-exceeded while the service stays fully
			// usable, so we report operational whenever any credential can serve.
			operational := "down"
			if cc.ok > 0 {
				operational = "operational"
			}

			samples, _ := store.ProviderHealthSamples(c.Request.Context(), pv.key, time.Unix(recentStart, 0).Add(-time.Duration(dailyDays)*24*time.Hour))

			// Recent 10-minute slots (a slot is "up" if ANY credential was ok in it).
			recent := make([]gin.H, recentSlots)
			rOK := make([]int, recentSlots)
			rTot := make([]int, recentSlots)
			// Daily rollup measures service uptime, NOT credential health: a day's
			// {ok,total} count 10-min slots where AT LEAST ONE credential was ok vs.
			// slots with any sample. Counting raw per-credential samples instead
			// would peg every day red whenever most creds sit dead/quota-exceeded
			// (e.g. 5 healthy of 17 → ratio≈0.29) even while the service stayed
			// fully usable — matching the "any cred ok" semantics of recent slots.
			type slotAgg struct{ okCount, total int }
			daySlots := map[int64]map[int64]*slotAgg{} // dayMidnight -> slotStart -> agg
			for _, s := range samples {
				ts := s.CheckedAt.Unix()
				if ts >= recentStart && ts <= nowU {
					idx := int((ts - recentStart) / recentSlotS)
					if idx >= 0 && idx < recentSlots {
						rTot[idx]++
						if s.Status == "ok" {
							rOK[idx]++
						}
					}
				}
				d := (ts / daySec) * daySec
				slot := (ts / recentSlotS) * recentSlotS
				m := daySlots[d]
				if m == nil {
					m = map[int64]*slotAgg{}
					daySlots[d] = m
				}
				ag := m[slot]
				if ag == nil {
					ag = &slotAgg{}
					m[slot] = ag
				}
				ag.total++
				if s.Status == "ok" {
					ag.okCount++
				}
			}
			for i := 0; i < recentSlots; i++ {
				recent[i] = gin.H{"from": recentStart + int64(i)*recentSlotS, "ok": rOK[i], "total": rTot[i]}
			}
			todayMidnight := (nowU / daySec) * daySec
			daily := make([]gin.H, dailyDays)
			for i := 0; i < dailyDays; i++ {
				d := todayMidnight - int64(dailyDays-1-i)*daySec
				upSlots, totSlots := 0, 0
				for _, ag := range daySlots[d] {
					totSlots++
					if ag.okCount > 0 {
						upSlots++
					}
				}
				daily[i] = gin.H{"date": d, "ok": upSlots, "total": totSlots}
			}

			// healthy_creds/total_creds are deliberately omitted: the public
			// status page reports service usability, not credential-pool size.
			out = append(out, gin.H{
				"key":         pv.key,
				"name":        pv.name,
				"operational": operational,
				"checked_at":  cc.checkedAt,
				"recent":      recent,
				"daily":       daily,
			})
		}
		c.JSON(http.StatusOK, gin.H{"providers": out, "as_of": nowU})
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
	if growthH != nil {
		growthH.AdminRoutes(adminG)
	}
	if analyticsH != nil {
		analyticsH.AdminRoutes(adminG)
	}
	if legacyH != nil {
		legacyH.RegisterSaaSBridge(adminG)
	}
}
