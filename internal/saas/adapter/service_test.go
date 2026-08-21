package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

const testSvcToken = "svc-secret-token"

type svcFixture struct {
	engine *gin.Engine
	store  *db.DB
	userID int64
	wsID   int64
	token  string
	tokID  int64
}

func newSvcFixture(t *testing.T, entries []string) *svcFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := db.Open(filepath.Join(t.TempDir(), "svc.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	u, err := store.CreateUser(ctx, "svc@example.com", "hash", db.RoleUser, 1, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.AddBalance(ctx, u.ID, db.TxKindTopup, 20, "seed", "", false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	wsID, err := store.PersonalWorkspaceID(ctx, u.ID)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	tok, err := store.CreateUserToken(ctx, u.ID, db.TokenParams{Name: "key1"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := NewAdapter(store, nil, nil)
	set, err := NewServiceTokenSet(entries)
	if err != nil {
		t.Fatalf("token set: %v", err)
	}
	engine := gin.New()
	NewServiceHandler(a, set).Mount(engine.Group("/api/v2"))
	return &svcFixture{engine: engine, store: store, userID: u.ID, wsID: wsID, token: tok.Token, tokID: tok.ID}
}

func (f *svcFixture) do(t *testing.T, method, path string, body any, tok string) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("X-Service-Token", tok)
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestServiceGroupAbsentWithoutTokens is the containment property: with
// saas.service_tokens empty, /api/v2/svc/* must not exist — not "exist and
// reject". A route that 401s still advertises that the money endpoints are
// there and still runs middleware on public traffic.
func TestServiceGroupAbsentWithoutTokens(t *testing.T) {
	f := newSvcFixture(t, nil)
	for _, path := range []string{"/api/v2/svc/resolve", "/api/v2/svc/charge", "/api/v2/svc/balance"} {
		code, _ := f.do(t, http.MethodPost, path, gin.H{}, testSvcToken)
		if code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404 (group must not be mounted)", path, code)
		}
	}
}

func TestRequireServiceRejectsBadToken(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	for _, tok := range []string{"", "wrong", testSvcToken + "x", " "} {
		code, _ := f.do(t, http.MethodPost, "/api/v2/svc/resolve", gin.H{"token": f.token}, tok)
		if code != http.StatusUnauthorized {
			t.Errorf("token %q = %d, want 401", tok, code)
		}
	}
	code, _ := f.do(t, http.MethodPost, "/api/v2/svc/resolve", gin.H{"token": f.token}, testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("valid token = %d, want 200", code)
	}
}

// TestServiceTokenSetAcceptsAllConfigForms: raw, sha256:<hex>, and @file must
// all authenticate the same secret — an operator keeping the secret out of git
// must not end up with an integration that silently doesn't authenticate.
func TestServiceTokenSetAcceptsAllConfigForms(t *testing.T) {
	sum := sha256.Sum256([]byte(testSvcToken))
	file := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(file, []byte("# comment\n\n"+testSvcToken+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	forms := map[string][]string{
		"raw":    {testSvcToken},
		"sha256": {"sha256:" + hex.EncodeToString(sum[:])},
		"file":   {"@" + file},
	}
	for name, entries := range forms {
		t.Run(name, func(t *testing.T) {
			set, err := NewServiceTokenSet(entries)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if !set.Match(testSvcToken) {
				t.Fatal("configured token did not match")
			}
			if set.Match("something-else") {
				t.Fatal("unrelated token matched")
			}
		})
	}
	if _, err := NewServiceTokenSet([]string{"sha256:nothex"}); err == nil {
		t.Fatal("malformed sha256 entry accepted")
	}
	if set, err := NewServiceTokenSet([]string{"", "   "}); err != nil || set != nil {
		t.Fatalf("blank-only list = (%v, %v), want (nil, nil)", set, err)
	}
}

func TestServiceResolve(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	code, body := f.do(t, http.MethodPost, "/api/v2/svc/resolve", gin.H{"token": f.token}, testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("resolve = %d %v", code, body)
	}
	for k, want := range map[string]any{
		"token_id":     float64(f.tokID),
		"user_id":      float64(f.userID),
		"workspace_id": float64(f.wsID),
		"email":        "svc@example.com",
		"name":         "key1",
		"balance_usd":  float64(20),
		"disabled":     false,
	} {
		if body[k] != want {
			t.Errorf("resolve[%q] = %v, want %v", k, body[k], want)
		}
	}
	if _, ok := body["groups"]; !ok {
		t.Error("resolve response missing groups")
	}
	code, _ = f.do(t, http.MethodPost, "/api/v2/svc/resolve", gin.H{"token": "no-such-key"}, testSvcToken)
	if code != http.StatusNotFound {
		t.Errorf("unknown token = %d, want 404", code)
	}
}

func TestServicePreCheck(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	code, body := f.do(t, http.MethodPost, "/api/v2/svc/precheck",
		gin.H{"token_id": f.tokID, "workspace_id": f.wsID}, testSvcToken)
	if code != http.StatusOK || body["ok"] != true {
		t.Fatalf("precheck = %d %v, want ok", code, body)
	}

	// Drain the wallet: the gate must now say 402 insufficient_balance, in the
	// body, still over HTTP 200 (the service call itself succeeded).
	ctx := context.Background()
	if _, _, err := f.store.ChargeWorkspaceWithFloor(ctx, f.wsID, f.userID, db.TxKindCharge, 20, "drain", "", 0, db.ChargeMeta{}); err != nil {
		t.Fatalf("drain: %v", err)
	}
	code, body = f.do(t, http.MethodPost, "/api/v2/svc/precheck",
		gin.H{"token_id": f.tokID, "workspace_id": f.wsID}, testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("precheck http = %d, want 200", code)
	}
	if body["ok"] != false || body["status"] != float64(http.StatusPaymentRequired) || body["code"] != "insufficient_balance" {
		t.Fatalf("precheck denial = %v", body)
	}

	// A workspace the key does not bill must be refused outright.
	code, _ = f.do(t, http.MethodPost, "/api/v2/svc/precheck",
		gin.H{"token_id": f.tokID, "workspace_id": f.wsID + 999}, testSvcToken)
	if code != http.StatusBadRequest {
		t.Errorf("mismatched workspace = %d, want 400", code)
	}
}

func (f *svcFixture) chargeBody(key string, amount float64) gin.H {
	return gin.H{
		"idempotency_key": key, "token_id": f.tokID, "user_id": f.userID,
		"workspace_id": f.wsID, "product": "hypihub", "model": "seedance-1-0-pro",
		"provider": "ark", "amount_usd": amount, "note": "1080p 5s",
		"counts": gin.H{"images": 0, "seconds": 5, "input_tokens": 0, "output_tokens": 0},
	}
}

// TestServiceChargeIsIdempotentOverHTTP is the end-to-end version of the
// property migration v22 exists for: the retry an HTTP client WILL make after a
// timeout must not bill twice.
func TestServiceChargeIsIdempotentOverHTTP(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	body := f.chargeBody("hypihub:job:job_abc", 0.412)

	code, first := f.do(t, http.MethodPost, "/api/v2/svc/charge", body, testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("charge = %d %v", code, first)
	}
	if first["charged_usd"] != 0.412 || first["replayed"] != false || first["clamped"] != false {
		t.Fatalf("charge response = %v", first)
	}
	if first["new_balance_usd"].(float64) > 19.589 || first["new_balance_usd"].(float64) < 19.587 {
		t.Fatalf("new_balance_usd = %v, want ~19.588", first["new_balance_usd"])
	}

	code, second := f.do(t, http.MethodPost, "/api/v2/svc/charge", body, testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("replay = %d %v", code, second)
	}
	if second["replayed"] != true {
		t.Fatalf("replay not flagged: %v", second)
	}
	if second["tx_id"] != first["tx_id"] || second["charged_usd"] != first["charged_usd"] {
		t.Fatalf("replay differs from original: %v vs %v", second, first)
	}
	bal, err := f.store.GetWorkspaceBalance(context.Background(), f.wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal < 19.587 || bal > 19.589 {
		t.Fatalf("balance %v — the retry billed twice", bal)
	}
}

func TestServiceRefund(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	code, charged := f.do(t, http.MethodPost, "/api/v2/svc/charge", f.chargeBody("hypihub:job:j1", 1.5), testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("charge = %d %v", code, charged)
	}
	txID := int64(charged["tx_id"].(float64))

	refund := gin.H{
		"idempotency_key": "hypihub:job:j1:refund", "tx_id": txID, "amount_usd": 1.5,
		"reason": "generation failed", "user_id": f.userID, "workspace_id": f.wsID,
	}
	code, first := f.do(t, http.MethodPost, "/api/v2/svc/refund", refund, testSvcToken)
	if code != http.StatusOK || first["replayed"] != false {
		t.Fatalf("refund = %d %v", code, first)
	}
	code, second := f.do(t, http.MethodPost, "/api/v2/svc/refund", refund, testSvcToken)
	if code != http.StatusOK || second["replayed"] != true || second["tx_id"] != first["tx_id"] {
		t.Fatalf("refund replay = %d %v", code, second)
	}
	bal, err := f.store.GetWorkspaceBalance(context.Background(), f.wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal < 19.999 || bal > 20.001 {
		t.Fatalf("balance %v after charge+refund, want 20", bal)
	}

	// A refund pointed at a charge belonging to another wallet is a transfer,
	// not a refund.
	bad := gin.H{
		"idempotency_key": "hypihub:job:j1:refund2", "tx_id": txID, "amount_usd": 1,
		"user_id": f.userID, "workspace_id": f.wsID + 999,
	}
	if code, _ := f.do(t, http.MethodPost, "/api/v2/svc/refund", bad, testSvcToken); code != http.StatusBadRequest {
		t.Errorf("cross-workspace refund = %d, want 400", code)
	}
}

// TestServiceChargeRejectsBadInput: reject, never clamp. Each case must leave
// the wallet untouched.
func TestServiceChargeRejectsBadInput(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	cases := map[string]gin.H{}

	noKey := f.chargeBody("", 1)
	cases["empty idempotency_key"] = noKey

	blankKey := f.chargeBody("   ", 1)
	cases["blank idempotency_key"] = blankKey

	longKey := f.chargeBody(string(bytes.Repeat([]byte("k"), maxIdemKeyLen+1)), 1)
	cases["oversized idempotency_key"] = longKey

	zero := f.chargeBody("k-zero", 0)
	cases["zero amount"] = zero

	neg := f.chargeBody("k-neg", -1)
	cases["negative amount"] = neg

	huge := f.chargeBody("k-huge", MaxServiceAmountUSD+0.01)
	cases["amount over ceiling"] = huge

	wrongWS := f.chargeBody("k-ws", 1)
	wrongWS["workspace_id"] = f.wsID + 999
	cases["workspace not owned by user"] = wrongWS

	noUser := f.chargeBody("k-nouser", 1)
	noUser["user_id"] = 0
	cases["missing user"] = noUser

	wrongTok := f.chargeBody("k-tok", 1)
	wrongTok["token_id"] = f.tokID + 999
	cases["unknown token"] = wrongTok

	for name, body := range cases {
		code, resp := f.do(t, http.MethodPost, "/api/v2/svc/charge", body, testSvcToken)
		if code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400 (%v)", name, code, resp)
		}
	}
	bal, err := f.store.GetWorkspaceBalance(context.Background(), f.wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal != 20 {
		t.Fatalf("rejected charges moved money: balance %v want 20", bal)
	}
}

func TestServiceBalanceAndUser(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	code, body := f.do(t, http.MethodGet, fmt.Sprintf("/api/v2/svc/balance?workspace_id=%d", f.wsID), nil, testSvcToken)
	if code != http.StatusOK || body["balance_usd"] != float64(20) {
		t.Fatalf("balance = %d %v", code, body)
	}
	if code, _ := f.do(t, http.MethodGet, "/api/v2/svc/balance", nil, testSvcToken); code != http.StatusBadRequest {
		t.Errorf("missing workspace_id = %d, want 400", code)
	}
	if code, _ := f.do(t, http.MethodGet, "/api/v2/svc/balance?workspace_id=99999", nil, testSvcToken); code != http.StatusNotFound {
		t.Errorf("unknown workspace = %d, want 404", code)
	}

	code, body = f.do(t, http.MethodGet, fmt.Sprintf("/api/v2/svc/user/%d", f.userID), nil, testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("user = %d %v", code, body)
	}
	if body["id"] != float64(f.userID) || body["email"] != "svc@example.com" ||
		body["role"] != db.RoleUser || body["disabled"] != false {
		t.Fatalf("user = %v", body)
	}
	if name, _ := body["name"].(string); name == "" {
		t.Error("user response carried no name")
	}
	if code, _ := f.do(t, http.MethodGet, "/api/v2/svc/user/99999", nil, testSvcToken); code != http.StatusNotFound {
		t.Errorf("unknown user = %d, want 404", code)
	}
}

// TestServiceChargeAcceptsFractionalSeconds pins the unit type on counts.
// Video duration is fractional, and a counts field that fails to decode takes
// the WHOLE body down with a blanket 400 — deterministically, so the caller's
// retry loop can never make progress and the charge for a generation that
// already cost real money upstream is lost forever.
func TestServiceChargeAcceptsFractionalSeconds(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	body := f.chargeBody("hypihub:job:vid1", 0.41)
	body["counts"] = gin.H{"images": 0, "seconds": 5.5, "input_tokens": 0, "output_tokens": 0}

	code, res := f.do(t, http.MethodPost, "/api/v2/svc/charge", body, testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("fractional seconds = %d %v, want 200", code, res)
	}
	if res["charged_usd"] != 0.41 {
		t.Fatalf("charged_usd = %v, want 0.41", res["charged_usd"])
	}
}

// TestServiceRefundIsBoundedByTheCharge is the money-printer guard: a refund
// may never credit more than the charge it names still has left to give back,
// no matter how the caller shuffles idempotency keys.
func TestServiceRefundIsBoundedByTheCharge(t *testing.T) {
	f := newSvcFixture(t, []string{testSvcToken})
	code, charged := f.do(t, http.MethodPost, "/api/v2/svc/charge", f.chargeBody("hypihub:job:jX", 0.41), testSvcToken)
	if code != http.StatusOK {
		t.Fatalf("charge = %d %v", code, charged)
	}
	txID := int64(charged["tx_id"].(float64))

	// (a) No tx_id at all: an unlinked credit is not a refund.
	code, res := f.do(t, http.MethodPost, "/api/v2/svc/refund", gin.H{
		"idempotency_key": "attacker:1", "amount_usd": 1000,
		"user_id": f.userID, "workspace_id": f.wsID, "reason": "no tx_id at all",
	}, testSvcToken)
	if code != http.StatusBadRequest {
		t.Fatalf("refund without tx_id = %d %v, want 400", code, res)
	}

	// (b) Over-refund of a real charge (the cents-vs-dollars unit slip).
	code, res = f.do(t, http.MethodPost, "/api/v2/svc/refund", gin.H{
		"idempotency_key": "hypihub:job:jX:refund", "tx_id": txID, "amount_usd": 41.0,
		"user_id": f.userID, "workspace_id": f.wsID,
	}, testSvcToken)
	if code != http.StatusBadRequest {
		t.Fatalf("over-refund = %d %v, want 400", code, res)
	}

	// (c) Partial refunds sum: half is fine, then the same half again is fine
	// (it exhausts the charge), then anything more is refused even under a
	// fresh key — the re-queued-job failure mode.
	half := gin.H{
		"idempotency_key": "hypihub:job:jX:refund:1", "tx_id": txID, "amount_usd": 0.205,
		"user_id": f.userID, "workspace_id": f.wsID,
	}
	if code, res = f.do(t, http.MethodPost, "/api/v2/svc/refund", half, testSvcToken); code != http.StatusOK {
		t.Fatalf("first half refund = %d %v", code, res)
	}
	// The retry of that same key must still replay, not trip the bound.
	if code, res = f.do(t, http.MethodPost, "/api/v2/svc/refund", half, testSvcToken); code != http.StatusOK || res["replayed"] != true {
		t.Fatalf("half refund replay = %d %v", code, res)
	}
	half["idempotency_key"] = "hypihub:job:jX:refund:2"
	if code, res = f.do(t, http.MethodPost, "/api/v2/svc/refund", half, testSvcToken); code != http.StatusOK {
		t.Fatalf("second half refund = %d %v", code, res)
	}
	half["idempotency_key"] = "hypihub:job:jX:refund:3"
	if code, res = f.do(t, http.MethodPost, "/api/v2/svc/refund", half, testSvcToken); code != http.StatusBadRequest {
		t.Fatalf("third half refund = %d %v, want 400", code, res)
	}

	bal, err := f.store.GetWorkspaceBalance(context.Background(), f.wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal < 19.999 || bal > 20.001 {
		t.Fatalf("balance %v after charge + full refund, want 20 — credit was minted", bal)
	}
}
