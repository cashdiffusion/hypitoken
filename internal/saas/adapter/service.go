// Service-to-service routes: /api/v2/svc/*.
//
// The sibling HypiHub gateway (image/video generation) shares this product's
// user accounts and USD wallet, but deliberately does NOT open saas.db — a
// second process writing the same SQLite file would race the migration runner,
// which refuses to start against a schema it doesn't recognise, and would put a
// second writer on the ledger that this repo's _txlock=immediate discipline
// cannot see. Instead HypiHub verifies hypitoken-issued JWTs offline with the
// shared HS256 secret, and moves money through the endpoints below.
//
// Everything here is additive. No existing route, table, or function changes
// behaviour, and with saas.service_tokens empty the group is never mounted.
package adapter

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// MaxServiceAmountUSD is the ceiling on a single service-billed movement.
//
// Not a business limit — a structural one. The caller computes the amount from
// a model price table and a duration/resolution, so a unit slip (cents read as
// dollars), a bad parse, or a runaway retry loop is the realistic failure mode,
// and the blast radius of that mistake is somebody's whole wallet plus the
// overdraft floor. Rejected, never clamped: a clamped charge would tell the
// caller the bogus amount was accepted.
const MaxServiceAmountUSD = 1000.0

// maxIdemKeyLen bounds the key that lands in an indexed TEXT column.
const maxIdemKeyLen = 200

// ServiceTokenSet holds the configured service credentials as SHA-256 digests.
//
// Hashed on load, so a raw token from the YAML never sits in memory as a
// comparable secret and both accepted config forms (raw / "sha256:<hex>")
// collapse to one representation — the same shape internal/server/
// relay_ingress.go uses for trusted_relay_tokens. The comparison itself is
// crypto/subtle over the digests and scans the whole set without an early exit,
// so neither the value nor the position of a matching entry leaks through
// timing.
type ServiceTokenSet struct {
	digests [][sha256.Size]byte
}

// serviceWarnOnce keeps the startup warning to one line even though Mount runs
// once per gin engine (Claude :8317 and Codex :8318 both get /api/v2).
var serviceWarnOnce sync.Once

// NewServiceTokenSet resolves the configured entries. Each may be:
//
//	<raw token>        — hashed here
//	sha256:<64 hex>    — already hashed; the config never holds the secret
//	@/path/to/file     — read the file, one entry per line (# comments allowed)
//
// Returns nil (no error) when the list is empty or contains only blanks: the
// caller treats that as "the service integration is off" and skips the mount
// entirely, so a deployment that doesn't run HypiHub has no such routes at all.
func NewServiceTokenSet(entries []string) (*ServiceTokenSet, error) {
	set := &ServiceTokenSet{}
	for _, raw := range entries {
		if err := set.add(raw, true); err != nil {
			return nil, err
		}
	}
	if len(set.digests) == 0 {
		return nil, nil
	}
	serviceWarnOnce.Do(func() {
		log.Warnf("saas: /api/v2/svc/* service-to-service routes are ENABLED (%d token(s)). "+
			"A holder of one of these tokens can debit ANY workspace — these routes MUST NOT be "+
			"reachable from the internet. Block /api/v2/svc/ at the reverse proxy and reach them "+
			"only over the private interface.", len(set.digests))
	})
	return set, nil
}

// add ingests one config entry. allowFile guards against an @file that points
// at another @file — one level of indirection is a convenience, a chain is a
// startup-time loop waiting to happen.
func (s *ServiceTokenSet) add(raw string, allowFile bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return nil
	}
	if path, ok := strings.CutPrefix(raw, "@"); ok {
		if !allowFile {
			return fmt.Errorf("saas.service_tokens: nested @file reference %q", raw)
		}
		// G703: the path is an operator-authored config value read once at
		// startup — the same @file convention as the payment-gateway secrets
		// (cmd/server/main.go loadKeyFile) — not attacker-controlled input.
		//nolint:gosec // G703 false positive — operator config, startup-only.
		data, err := os.ReadFile(strings.TrimSpace(path))
		if err != nil {
			return fmt.Errorf("saas.service_tokens: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if err := s.add(line, false); err != nil {
				return err
			}
		}
		return nil
	}
	if h, ok := strings.CutPrefix(raw, "sha256:"); ok {
		sum, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(h)))
		if err != nil || len(sum) != sha256.Size {
			return errors.New("saas.service_tokens: sha256: entry must be 64 hex characters")
		}
		var d [sha256.Size]byte
		copy(d[:], sum)
		s.digests = append(s.digests, d)
		return nil
	}
	s.digests = append(s.digests, sha256.Sum256([]byte(raw)))
	return nil
}

// Match reports whether tok is one of the configured service tokens.
func (s *ServiceTokenSet) Match(tok string) bool {
	if s == nil || len(s.digests) == 0 || tok == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	hit := 0
	for i := range s.digests {
		// No early break: every entry is compared on every call so the time
		// taken does not depend on which token (if any) matched.
		hit |= subtle.ConstantTimeCompare(sum[:], s.digests[i][:])
	}
	return hit == 1
}

// RequireService authenticates the sibling service by X-Service-Token.
//
// There is no user identity here and no fallback: a request either presents a
// configured service token or gets 401. Deliberately terse — the caller is a
// machine, and a verbose error on an unauthenticated money endpoint is a
// reconnaissance aid.
func RequireService(tokens *ServiceTokenSet) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !tokens.Match(strings.TrimSpace(c.GetHeader("X-Service-Token"))) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// ServiceHandler serves /api/v2/svc/*. It owns no state beyond the adapter it
// reads identity and billing rules from plus the credential set that guards it,
// so one instance is mounted on every gin engine.
type ServiceHandler struct {
	A      *Adapter
	Tokens *ServiceTokenSet

	// Iss signs the JWT handed back by /svc/sso/redeem. Set via WithIssuer
	// rather than through the constructor so adding cross-site SSO did not
	// change NewServiceHandler's signature. nil disables only that one route.
	Iss *saasauth.Issuer
}

// WithIssuer attaches the JWT issuer used by the SSO redeem route and returns
// h, for chaining at the call site. Nil-safe.
//
// It is the SAME issuer the login endpoint uses — deliberately. A session
// adopted across origins must be indistinguishable from one obtained by typing
// a password here: same secret, same TTL, same claims. A second issuer with its
// own lifetime would be a second, quietly-different notion of "logged in".
func (h *ServiceHandler) WithIssuer(iss *saasauth.Issuer) *ServiceHandler {
	if h == nil {
		return nil
	}
	h.Iss = iss
	return h
}

// NewServiceHandler returns nil when there is no adapter or no configured
// service token. A nil handler mounts nothing at all — that is how "the sibling
// service integration is off" is expressed, rather than mounting a group that
// rejects everything.
func NewServiceHandler(a *Adapter, tokens *ServiceTokenSet) *ServiceHandler {
	if a == nil || tokens == nil || len(tokens.digests) == 0 {
		return nil
	}
	return &ServiceHandler{A: a, Tokens: tokens}
}

// Mount registers the /svc group under v2, behind RequireService. Nil-safe: a
// nil handler registers nothing, so /api/v2/svc/* 404s like any unknown path.
func (h *ServiceHandler) Mount(v2 *gin.RouterGroup) {
	if h == nil {
		return
	}
	h.Routes(v2.Group("/svc", RequireService(h.Tokens)))
}

// Routes registers the service endpoints on an already-authenticated group.
func (h *ServiceHandler) Routes(g *gin.RouterGroup) {
	g.POST("/resolve", h.resolve)
	g.POST("/precheck", h.precheck)
	g.POST("/charge", h.charge)
	g.POST("/refund", h.refund)
	g.GET("/balance", h.balance)
	g.GET("/user/:id", h.user)
	// Cross-origin SSO handoff, redeeming half. Mounted with the rest of the
	// group: it is already gated by service_tokens, and it is the sibling
	// site — not a browser — that calls it.
	g.POST("/sso/redeem", h.ssoRedeem)
}

// svcCtx is the context every handler does its DB work under.
//
// context.WithoutCancel, not c.Request.Context(): a wallet movement must not be
// abandoned because the caller's socket dropped. This repo already learned that
// the expensive way on the proxy path — 14865 charges lost to "context
// canceled" over five days, requests served and billed to nobody (see
// internal/server/saas_adapter.go chargeCtx). An HTTP biller makes it worse,
// not better: the sibling's client timeout is exactly the moment we are most
// likely to be mid-commit. Applied to the read paths too, so no handler here
// can regress into the cancellable form by being copied.
func svcCtx(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return context.WithoutCancel(c.Request.Context())
}

func svcError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

// --- resolve ---------------------------------------------------------------

type svcResolveReq struct {
	Token string `json:"token"`
}

// svcTokenInfo is the wire shape of a resolved API key. Field names are frozen
// by the HypiHub contract (SPEC §5) — do not rename them.
type svcTokenInfo struct {
	TokenID       int64    `json:"token_id"`
	UserID        int64    `json:"user_id"`
	Email         string   `json:"email"`
	Name          string   `json:"name"`
	WorkspaceID   int64    `json:"workspace_id"`
	BalanceUSD    float64  `json:"balance_usd"`
	Disabled      bool     `json:"disabled"`
	Groups        []string `json:"groups"`
	RPM           int      `json:"rpm"`
	MaxConcurrent int      `json:"max_concurrent"`
}

// resolve turns a customer's "sk-cpa-…" key into the identity + billing subject
// HypiHub needs. 404 means the key does not exist; a key that exists but is
// disabled resolves normally with disabled=true, so the caller can tell "no
// such key" from "your key is switched off" and say so.
func (h *ServiceHandler) resolve(c *gin.Context) {
	var req svcResolveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		svcError(c, http.StatusBadRequest, "bad_request", "malformed JSON body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		svcError(c, http.StatusBadRequest, "bad_request", "token is required")
		return
	}
	info, ok := h.A.LookupCtx(svcCtx(c), token)
	if !ok {
		svcError(c, http.StatusNotFound, "invalid_token", "no such API key")
		return
	}
	groups := info.Groups
	if groups == nil {
		groups = []string{}
	}
	c.JSON(http.StatusOK, svcTokenInfo{
		TokenID:       info.TokenID,
		UserID:        info.UserID,
		Email:         info.Email,
		Name:          info.Name,
		WorkspaceID:   info.WorkspaceID,
		BalanceUSD:    info.BalanceUSD,
		Disabled:      info.Disabled,
		Groups:        groups,
		RPM:           info.RPM,
		MaxConcurrent: info.MaxConcurrent,
	})
}

// --- precheck --------------------------------------------------------------

type svcPreCheckReq struct {
	TokenID     int64 `json:"token_id"`
	WorkspaceID int64 `json:"workspace_id"`
}

// precheck runs the SAME gate the proxy runs before forwarding: disabled
// account, zero balance, then the stacked workspace / member / per-key caps.
// Shared deliberately — a customer who is out of credit must be out of credit
// on both products, and a second implementation would drift.
//
// Always HTTP 200 when the gate ran: the answer lives in the body, with the
// status the CALLER should return to ITS client. A non-200 here means the
// service call itself failed.
func (h *ServiceHandler) precheck(c *gin.Context) {
	var req svcPreCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		svcError(c, http.StatusBadRequest, "bad_request", "malformed JSON body")
		return
	}
	ctx := svcCtx(c)
	info, ok := h.A.LookupByTokenID(ctx, req.TokenID)
	if !ok {
		svcError(c, http.StatusNotFound, "invalid_token", "no such API key")
		return
	}
	// The caller states which wallet it believes it is spending. If that
	// disagrees with the key's actual billing subject, something is stale or
	// forged — refuse rather than authorize against the wrong pool.
	if req.WorkspaceID != 0 && req.WorkspaceID != info.WorkspaceID {
		svcError(c, http.StatusBadRequest, "workspace_mismatch",
			"workspace_id does not match the billing workspace of this API key")
		return
	}
	if pce := h.A.PreCheck(ctx, info); pce != nil {
		out := gin.H{"ok": false, "status": pce.Status, "code": pce.Code, "message": pce.Message}
		for k, v := range pce.Details {
			out[k] = v
		}
		c.JSON(http.StatusOK, out)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- charge / refund -------------------------------------------------------

// svcCounts mirrors core.Units on the caller's side (hypihub docs/SPEC.md
// §2.1). Seconds is FLOAT — video duration is routinely fractional, and typing
// it int64 here made ShouldBindJSON reject the whole body, which the handler
// reports as a blanket 400 before any money moves. That failure is
// deterministic, so the caller's retry loop can never make progress and the
// charge for a generation that already cost real money upstream is lost.
type svcCounts struct {
	Images       int64   `json:"images"`
	Seconds      float64 `json:"seconds"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

type svcChargeReq struct {
	IdempotencyKey string    `json:"idempotency_key"`
	TokenID        int64     `json:"token_id"`
	UserID         int64     `json:"user_id"`
	WorkspaceID    int64     `json:"workspace_id"`
	Product        string    `json:"product"`
	Model          string    `json:"model"`
	Provider       string    `json:"provider"`
	AmountUSD      float64   `json:"amount_usd"`
	Note           string    `json:"note"`
	Counts         svcCounts `json:"counts"`
}

type svcRefundReq struct {
	IdempotencyKey string  `json:"idempotency_key"`
	TxID           int64   `json:"tx_id"`
	AmountUSD      float64 `json:"amount_usd"`
	Reason         string  `json:"reason"`
	UserID         int64   `json:"user_id"`
	WorkspaceID    int64   `json:"workspace_id"`
	Product        string  `json:"product"`
}

type svcChargeResp struct {
	ChargedUSD    float64 `json:"charged_usd"`
	NewBalanceUSD float64 `json:"new_balance_usd"`
	Clamped       bool    `json:"clamped"`
	TxID          int64   `json:"tx_id"`
	Replayed      bool    `json:"replayed"`
}

// charge debits the wallet, exactly once per idempotency_key.
//
// The amount arrives ALREADY BILLED — HypiHub owns its own price table and its
// own margin, so no multiplier is applied here (the claude/codex multipliers
// are properties of the proxy's pricing, not of image generation). What this
// layer still owns is the overdraft floor, so a generation that already cost
// real money upstream is billed as far as the wallet allows and no further.
func (h *ServiceHandler) charge(c *gin.Context) {
	var req svcChargeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		svcError(c, http.StatusBadRequest, "bad_request", "malformed JSON body")
		return
	}
	ctx := svcCtx(c)
	key, msg := validIdemKey(req.IdempotencyKey)
	if msg != "" {
		svcError(c, http.StatusBadRequest, "bad_request", msg)
		return
	}
	if msg := validAmount(req.AmountUSD); msg != "" {
		svcError(c, http.StatusBadRequest, "bad_request", msg)
		return
	}
	if msg := h.validSubject(ctx, req.UserID, req.WorkspaceID, req.TokenID); msg != "" {
		svcError(c, http.StatusBadRequest, "subject_mismatch", msg)
		return
	}
	res, err := h.A.DB.ChargeWorkspaceIdem(ctx, db.IdemChargeReq{
		IdempotencyKey:  key,
		Product:         product(req.Product),
		WorkspaceID:     req.WorkspaceID,
		UserID:          req.UserID,
		AmountUSD:       req.AmountUSD,
		Ref:             chargeRef(req),
		Note:            trunc(req.Note, 500),
		MaxOverdraftUSD: h.A.MaxOverdraftUSD,
		Meta: db.ChargeMeta{
			TokenID:      req.TokenID,
			Model:        trunc(req.Model, 200),
			InputTokens:  req.Counts.InputTokens,
			OutputTokens: req.Counts.OutputTokens,
		},
	})
	h.writeChargeResult(c, res, err)
}

// refund credits the wallet back, exactly once per idempotency_key. The refund
// key must differ from the charge key it reverses (the contract suggests the
// charge key plus ":refund"); reusing the charge key is refused by the ledger
// as a kind mismatch rather than silently replaying the debit.
func (h *ServiceHandler) refund(c *gin.Context) {
	var req svcRefundReq
	if err := c.ShouldBindJSON(&req); err != nil {
		svcError(c, http.StatusBadRequest, "bad_request", "malformed JSON body")
		return
	}
	ctx := svcCtx(c)
	key, msg := validIdemKey(req.IdempotencyKey)
	if msg != "" {
		svcError(c, http.StatusBadRequest, "bad_request", msg)
		return
	}
	if msg := validAmount(req.AmountUSD); msg != "" {
		svcError(c, http.StatusBadRequest, "bad_request", msg)
		return
	}
	if msg := h.validSubject(ctx, req.UserID, req.WorkspaceID, 0); msg != "" {
		svcError(c, http.StatusBadRequest, "subject_mismatch", msg)
		return
	}
	// A refund names the charge it reverses, and the linkage is MANDATORY: it
	// is the only thing bounding a credit. Debits are capped by what the
	// wallet holds; without this, credits are capped by nothing but the
	// per-call ceiling, so /svc/refund would be a money printer for anything
	// that reaches it (a leaked service token, an SSRF, or simply a caller bug
	// that drops tx_id on a retry).
	if req.TxID <= 0 {
		svcError(c, http.StatusBadRequest, "bad_request", "tx_id is required")
		return
	}
	// Verify that row is real and bills the same wallet — a refund pointed at
	// someone else's charge would be a transfer between wallets wearing a
	// refund's clothes.
	var (
		wsID   int64
		kind   string
		amount float64
	)
	if err := h.A.DB.QueryRowContext(ctx,
		`SELECT workspace_id, kind, amount_usd FROM wallet_tx WHERE id = ?`,
		req.TxID).Scan(&wsID, &kind, &amount); err != nil {
		svcError(c, http.StatusBadRequest, "unknown_tx", "tx_id does not exist")
		return
	}
	if wsID != req.WorkspaceID || kind != db.TxKindCharge {
		svcError(c, http.StatusBadRequest, "subject_mismatch",
			"tx_id is not a charge against this workspace")
		return
	}
	// ...and that the charge has that much value left to give back. Charges
	// are stored NEGATIVE, so the magnitude is what was actually collected
	// (already net of any overdraft clamp). Prior refunds are summed off the
	// `refund=<tx_id>` ref written below, excluding this very key so that a
	// retry of the SAME refund still replays through the ledger instead of
	// tripping the bound. Without this sum, distinct idempotency keys — a
	// re-queued job carrying a new job id, say — each credit in full.
	refundRef := fmt.Sprintf("refund=%d", req.TxID)
	var already float64
	if err := h.A.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount_usd), 0) FROM wallet_tx WHERE kind = ? AND ref = ? AND idem_key <> ?`,
		db.TxKindRefund, refundRef, key).Scan(&already); err != nil {
		log.Errorf("saas svc: refund history lookup failed: %v", err)
		svcError(c, http.StatusInternalServerError, "wallet_error", "wallet movement failed")
		return
	}
	remaining := -amount - already
	if remaining < 0 {
		remaining = 0
	}
	if req.AmountUSD > remaining+1e-9 {
		svcError(c, http.StatusBadRequest, "refund_exceeds_charge",
			fmt.Sprintf("refund exceeds the unrefunded remainder of tx %d ($%.4f)", req.TxID, remaining))
		return
	}
	note := trunc(strings.TrimSpace(req.Reason), 500)
	res, err := h.A.DB.CreditWorkspaceIdem(ctx, db.IdemChargeReq{
		IdempotencyKey: key,
		Product:        product(req.Product),
		WorkspaceID:    req.WorkspaceID,
		UserID:         req.UserID,
		AmountUSD:      req.AmountUSD,
		Ref:            refundRef,
		Note:           note,
	})
	h.writeChargeResult(c, res, err)
}

func (h *ServiceHandler) writeChargeResult(c *gin.Context, res db.IdemChargeResult, err error) {
	switch {
	case err == nil:
	case errors.Is(err, db.ErrIdemConflict):
		svcError(c, http.StatusConflict, "idempotency_conflict",
			"this idempotency_key was already used for a different movement")
		return
	case errors.Is(err, db.ErrNotFound):
		svcError(c, http.StatusNotFound, "unknown_workspace", "no such workspace")
		return
	default:
		log.Errorf("saas svc: wallet movement failed: %v", err)
		svcError(c, http.StatusInternalServerError, "wallet_error", "wallet movement failed")
		return
	}
	c.JSON(http.StatusOK, svcChargeResp{
		ChargedUSD:    res.ChargedUSD,
		NewBalanceUSD: res.NewBalanceUSD,
		Clamped:       res.Clamped,
		TxID:          res.TxID,
		Replayed:      res.Replayed,
	})
}

// --- balance / user --------------------------------------------------------

func (h *ServiceHandler) balance(c *gin.Context) {
	wsID, err := strconv.ParseInt(strings.TrimSpace(c.Query("workspace_id")), 10, 64)
	if err != nil || wsID <= 0 {
		svcError(c, http.StatusBadRequest, "bad_request", "workspace_id is required")
		return
	}
	bal, err := h.A.DB.GetWorkspaceBalance(svcCtx(c), wsID)
	if err != nil {
		svcError(c, http.StatusNotFound, "unknown_workspace", "no such workspace")
		return
	}
	c.JSON(http.StatusOK, gin.H{"balance_usd": bal})
}

func (h *ServiceHandler) user(c *gin.Context) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		svcError(c, http.StatusBadRequest, "bad_request", "invalid user id")
		return
	}
	ctx := svcCtx(c)
	u, err := h.A.DB.GetUser(ctx, id)
	if err != nil {
		svcError(c, http.StatusNotFound, "unknown_user", "no such user")
		return
	}
	// Display name comes from the arena profile, the same source /api/v2/me
	// greets the user by. Best-effort: a profile read failure must not turn a
	// user lookup into a 500.
	name := ""
	if p, perr := h.A.DB.GetOrCreateProfile(ctx, u.ID); perr == nil {
		name = p.DisplayName
	}
	c.JSON(http.StatusOK, gin.H{
		"id": u.ID, "email": u.Email, "name": name,
		"role": u.Role, "disabled": u.Disabled,
	})
}

// --- sso redeem ------------------------------------------------------------

// ssoRedeemMessage is the ONE thing this endpoint says when anything goes
// wrong: unknown code, expired code, spent code, deleted user, disabled user.
//
// Uniform by design. The caller is a trusted sibling that only ever presents
// codes it just received, so it has no legitimate use for the distinction —
// while anyone else who reaches this endpoint would learn from a specific error
// which codes were real, whether a leaked code was intercepted in time, and
// which user ids exist. 404 rather than 401/403 for the same reason: as far as
// an unauthorized caller can tell, there was nothing here.
const ssoRedeemMessage = "invalid or expired code"

type svcSSORedeemReq struct {
	Code string `json:"code"`
}

// ssoRedeem spends a one-time handoff code and returns a freshly-signed JWT.
//
// The user is re-loaded and re-checked here rather than trusted from the code:
// a code minted 120 seconds ago says who asked for it, not whether that account
// is still allowed in. An operator who disables an account mid-handoff must win.
func (h *ServiceHandler) ssoRedeem(c *gin.Context) {
	var req svcSSORedeemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		svcError(c, http.StatusNotFound, "invalid_code", ssoRedeemMessage)
		return
	}
	if h.Iss == nil {
		// Wiring bug, not a request failure — surfaced loudly in the log
		// because the wire answer has to stay uniform.
		log.Error("saas svc: /svc/sso/redeem called with no JWT issuer configured")
		svcError(c, http.StatusNotFound, "invalid_code", ssoRedeemMessage)
		return
	}

	// WithoutCancel like every other handler here: redeeming is a WRITE that
	// spends the code. If the caller's socket drops after the commit, the code
	// is gone either way — so the commit must not be the thing that gets
	// abandoned halfway, leaving a code marked used with no token issued.
	ctx := svcCtx(c)

	userID, _, err := h.A.DB.RedeemSSOCode(ctx, strings.TrimSpace(req.Code))
	if err != nil {
		if !errors.Is(err, db.ErrSSOCodeInvalid) {
			log.Errorf("saas svc: sso redeem failed: %v", err)
		}
		svcError(c, http.StatusNotFound, "invalid_code", ssoRedeemMessage)
		return
	}

	u, err := h.A.DB.GetUser(ctx, userID)
	if err != nil || u == nil || u.Disabled {
		svcError(c, http.StatusNotFound, "invalid_code", ssoRedeemMessage)
		return
	}

	tok, _, err := h.Iss.Issue(u.ID, u.Role)
	if err != nil {
		log.Errorf("saas svc: signing sso jwt for user %d failed: %v", u.ID, err)
		svcError(c, http.StatusNotFound, "invalid_code", ssoRedeemMessage)
		return
	}

	// Display name from the arena profile, same source as /svc/user/:id and
	// /api/v2/me. Best-effort — a profile read failure must not cost the user
	// their session.
	name := ""
	if p, perr := h.A.DB.GetOrCreateProfile(ctx, u.ID); perr == nil {
		name = p.DisplayName
	}

	c.JSON(http.StatusOK, gin.H{
		"token": tok,
		"user": gin.H{
			"id": u.ID, "email": u.Email, "name": name, "role": u.Role,
		},
	})
}

// --- validation ------------------------------------------------------------

// validIdemKey returns the trimmed key, or a reason to reject. An empty key is
// refused rather than defaulted: without one the whole replay guard is off, and
// a caller that forgot to send one would be double-charging silently.
func validIdemKey(raw string) (string, string) {
	key := strings.TrimSpace(raw)
	switch {
	case key == "":
		return "", "idempotency_key is required"
	case len(key) > maxIdemKeyLen:
		return "", fmt.Sprintf("idempotency_key exceeds %d bytes", maxIdemKeyLen)
	}
	return key, ""
}

// validAmount rejects — never clamps. A clamped amount would be reported back
// as accepted, which is how a pricing bug becomes an invisible pricing bug.
func validAmount(amount float64) string {
	switch {
	case math.IsNaN(amount) || math.IsInf(amount, 0):
		return "amount_usd must be a finite number"
	case amount <= 0:
		return "amount_usd must be greater than zero"
	case amount > MaxServiceAmountUSD:
		return fmt.Sprintf("amount_usd exceeds the per-call ceiling of $%.0f", MaxServiceAmountUSD)
	}
	return ""
}

// validSubject verifies the caller's (user, workspace[, token]) triple is
// internally consistent before any money moves. The service token authorizes
// the CALLER, not any particular wallet, so this is the only thing standing
// between a stale job record and a debit against the wrong customer.
//
// Membership is the test: every user is an admin member of their own personal
// workspace (CreatePersonalWorkspace guarantees it), and an enterprise member
// who has been removed must no longer be able to spend the shared pool.
func (h *ServiceHandler) validSubject(ctx context.Context, userID, workspaceID, tokenID int64) string {
	if userID <= 0 {
		return "user_id is required"
	}
	if workspaceID <= 0 {
		return "workspace_id is required"
	}
	if _, err := h.A.DB.GetWorkspaceMember(ctx, workspaceID, userID); err != nil {
		return "user_id is not a member of workspace_id"
	}
	if tokenID > 0 {
		t, err := h.A.DB.GetUserToken(ctx, tokenID)
		if err != nil {
			return "token_id does not exist"
		}
		if t.UserID != userID {
			return "token_id does not belong to user_id"
		}
		// A legacy token with workspace_id = 0 bills its owner's personal
		// workspace; anything else must match exactly.
		if t.WorkspaceID != 0 && t.WorkspaceID != workspaceID {
			return "token_id does not bill workspace_id"
		}
	}
	return ""
}

// chargeRef mirrors the proxy's legacy "token=<id> model=<name>" ledger ref so
// /billing/transactions and the workspace ledger render a HypiHub row the same
// way they render a proxy row, and the provider is kept alongside it.
func chargeRef(req svcChargeReq) string {
	ref := fmt.Sprintf("token=%d model=%s", req.TokenID, trunc(req.Model, 200))
	if p := trunc(strings.TrimSpace(req.Provider), 60); p != "" {
		ref += " provider=" + p
	}
	return ref
}

// product defaults an unnamed biller to "hypihub": everything reaching these
// routes is a sibling service, and "" is reserved for the proxy's own rows.
func product(p string) string {
	if p = trunc(strings.TrimSpace(p), 40); p != "" {
		return p
	}
	return "hypihub"
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
