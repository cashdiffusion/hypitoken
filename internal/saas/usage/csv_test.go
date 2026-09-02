package usage

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// exportFixture builds a workspace with one key that has spent money, then runs
// the team CSV export against it and returns the raw bytes.
func exportFixture(t *testing.T, keyName string, tags []string) ([]byte, *db.UserToken) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	d := testDB(t)

	ws, err := d.CreateEnterpriseWorkspace(ctx, "acme", 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	// Funded directly: the test wants a balance, not a ledger entry, and
	// the constructor no longer mints one.
	if _, err := d.ExecContext(ctx, `UPDATE workspaces SET balance_usd = ? WHERE id = ?`, 1000, ws.ID); err != nil {
		t.Fatalf("fund workspace: %v", err)
	}
	u, err := d.CreateUser(ctx, "member@acme.com", "hash", "user", 1, true)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	key, err := d.CreateUserToken(ctx, u.ID, db.TokenParams{Name: keyName, WorkspaceID: ws.ID, Tags: tags})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, _, err := d.ChargeWorkspaceWithFloor(ctx, ws.ID, u.ID, db.TxKindCharge, 1.25,
		"token=1 model=claude-opus-4-8", "", 0,
		db.ChargeMeta{TokenID: key.ID, Model: "claude-opus-4-8", InputTokens: 500, OutputTokens: 80}); err != nil {
		t.Fatalf("charge: %v", err)
	}

	h := New(d)
	r := gin.New()
	g := r.Group("/workspaces/:id/usage")
	h.TeamRoutes(g)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+itoa(ws.ID)+"/usage/export.csv", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", w.Code, w.Body.String())
	}
	return w.Body.Bytes(), key
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Without a BOM, Excel on Windows reads a UTF-8 CSV as GBK and every Chinese tag
// becomes mojibake. This is the single most likely thing to generate a support
// ticket from a Chinese enterprise customer, so it gets its own test.
func TestExportHasUTF8BOM(t *testing.T) {
	body, _ := exportFixture(t, "研发-张三", []string{"研发部"})
	if !bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("CSV does not start with a UTF-8 BOM: % x", body[:min(8, len(body))])
	}
	// And the Chinese content survives the round trip intact.
	if !bytes.Contains(body, []byte("研发部")) {
		t.Error("Chinese tag missing from export")
	}
}

// The export must never carry a key secret. A space admin is a customer, not an
// operator.
func TestExportNeverLeaksTokenSecret(t *testing.T) {
	body, key := exportFixture(t, "corp-key", nil)
	if bytes.Contains(body, []byte("sk-cpa-")) {
		t.Fatal("export contains a raw key secret")
	}
	if bytes.Contains(body, []byte(key.Token)) {
		t.Fatal("export contains the key's token value")
	}
}

// Key names and tags are user-authored and land in Excel, where a leading =, +, -
// or @ is evaluated as a formula.
func TestExportEscapesFormulaInjection(t *testing.T) {
	body, _ := exportFixture(t, `=cmd|'/c calc'!A0`, []string{"@SUM(1+1)"})
	text := string(body)
	if !strings.Contains(text, `'=cmd`) {
		t.Errorf("formula-shaped key name not neutralized:\n%s", text)
	}
	if !strings.Contains(text, `'@SUM`) {
		t.Errorf("formula-shaped tag not neutralized:\n%s", text)
	}
}

func TestExportColumnsAndValues(t *testing.T) {
	body, key := exportFixture(t, "alice-laptop", []string{"研发部", "前端"})
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF}))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows (incl. header), want 2", len(rows))
	}

	head := rows[0]
	col := func(name string) string {
		for i, h := range head {
			if h == name {
				return rows[1][i]
			}
		}
		t.Fatalf("column %q missing from header %v", name, head)
		return ""
	}

	if got := col("member_email"); got != "member@acme.com" {
		t.Errorf("member_email = %q", got)
	}
	if got := col("token_id"); got != itoa(key.ID) {
		t.Errorf("token_id = %q, want %d", got, key.ID)
	}
	if got := col("token_name"); got != "alice-laptop" {
		t.Errorf("token_name = %q", got)
	}
	// "|" not "," so a naive re-split on comma can't mangle a multi-tag cell.
	if got := col("tags"); got != "研发部|前端" {
		t.Errorf("tags = %q, want 研发部|前端", got)
	}
	if got := col("model"); got != "claude-opus-4-8" {
		t.Errorf("model = %q", got)
	}
	// Six decimal places: charges are routinely sub-cent.
	if got := col("amount_usd"); got != "1.250000" {
		t.Errorf("amount_usd = %q, want 1.250000", got)
	}
	if got := col("input_tokens"); got != "500" {
		t.Errorf("input_tokens = %q", got)
	}
}

// A pre-v15 row has no recorded token counts. Those cells must be BLANK, not 0 —
// a reader has to be able to tell "this request used no tokens" from "we didn't
// record it back then".
func TestExportPreV15CountsAreBlankNotZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	d := testDB(t)

	ws, err := d.CreateEnterpriseWorkspace(ctx, "acme", 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	// Funded directly: the test wants a balance, not a ledger entry, and
	// the constructor no longer mints one.
	if _, err := d.ExecContext(ctx, `UPDATE workspaces SET balance_usd = ? WHERE id = ?`, 1000, ws.ID); err != nil {
		t.Fatalf("fund workspace: %v", err)
	}
	u, err := d.CreateUser(ctx, "old@acme.com", "hash", "user", 1, true)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	// A v14-era row: ref only, no structured attribution, no counts.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO wallet_tx (user_id, workspace_id, kind, amount_usd, ref, note, created_at)
		 VALUES (?, ?, 'charge', -0.5, 'legacy', '', strftime('%s','now'))`, u.ID, ws.ID); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	h := New(d)
	r := gin.New()
	h.TeamRoutes(r.Group("/workspaces/:id/usage"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workspaces/"+itoa(ws.ID)+"/usage/export.csv", nil))

	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(w.Body.Bytes(), []byte{0xEF, 0xBB, 0xBF}))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	head, row := rows[0], rows[1]
	for i, h := range head {
		switch h {
		case "input_tokens", "output_tokens", "cache_read_tokens", "cache_create_tokens":
			if row[i] != "" {
				t.Errorf("%s = %q for an unrecorded pre-v15 row; want blank", h, row[i])
			}
		case "token_name":
			if row[i] != "(unattributed)" {
				t.Errorf("token_name = %q, want (unattributed)", row[i])
			}
		}
	}
}
