package db

import (
	"context"
	"testing"
	"time"
)

// charge is a test helper: bill `amt` to `ws` on behalf of `uid` using key `tok`.
func charge(t *testing.T, d *DB, ws, uid, tok int64, model string, amt float64) {
	t.Helper()
	ctx := context.Background()
	ref := "token=" + itoa(tok) + " model=" + model
	if _, _, err := d.ChargeWorkspaceWithFloor(ctx, ws, uid, TxKindCharge, amt, ref, "", 0,
		ChargeMeta{TokenID: tok, Model: model, InputTokens: 100, OutputTokens: 20}); err != nil {
		t.Fatalf("charge: %v", err)
	}
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

func seedEnterprise(t *testing.T, d *DB, name string) int64 {
	t.Helper()
	ws, err := d.CreateEnterpriseWorkspace(context.Background(), name, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create enterprise ws: %v", err)
	}
	// Funded directly: the test wants a balance, not a ledger entry, and
	// the constructor no longer mints one.
	if _, err := d.ExecContext(context.Background(), `UPDATE workspaces SET balance_usd = ? WHERE id = ?`, 1000, ws.ID); err != nil {
		t.Fatalf("fund workspace: %v", err)
	}
	return ws.ID
}

func TestSpendByTokenAggregates(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws := seedEnterprise(t, d, "acme")
	alice := mkUser(t, d, "alice@acme.com")
	bob := mkUser(t, d, "bob@acme.com")

	ka, err := d.CreateUserToken(ctx, alice, TokenParams{Name: "alice-laptop", WorkspaceID: ws})
	if err != nil {
		t.Fatalf("key a: %v", err)
	}
	kb, err := d.CreateUserToken(ctx, bob, TokenParams{Name: "bob-ci", WorkspaceID: ws})
	if err != nil {
		t.Fatalf("key b: %v", err)
	}

	charge(t, d, ws, alice, ka.ID, "claude-opus-4-8", 3.0)
	charge(t, d, ws, alice, ka.ID, "claude-opus-4-8", 2.0)
	charge(t, d, ws, bob, kb.ID, "gpt-5-codex", 1.5)

	got, err := d.SpendByToken(ctx, ReportFilter{Scope: WorkspaceScope(ws)})
	if err != nil {
		t.Fatalf("SpendByToken: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d keys, want 2", len(got))
	}
	// Biggest spender first.
	if got[0].TokenID != ka.ID || !approxEq(got[0].SpentUSD, 5.0) || got[0].ChargeEvents != 2 {
		t.Errorf("top key = %+v; want token %d, $5.00, 2 events", got[0], ka.ID)
	}
	if got[0].Email != "alice@acme.com" || got[0].Name != "alice-laptop" {
		t.Errorf("owner not resolved: email=%q name=%q", got[0].Email, got[0].Name)
	}
	if got[1].TokenID != kb.ID || !approxEq(got[1].SpentUSD, 1.5) {
		t.Errorf("second key = %+v; want token %d, $1.50", got[1], kb.ID)
	}

	// The per-key breakdown must reconcile with the workspace total, or finance
	// won't trust any of it.
	total, err := d.SumChargeSinceForWorkspace(ctx, ws, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("workspace total: %v", err)
	}
	sum := 0.0
	for _, k := range got {
		sum += k.SpentUSD
	}
	if !approxEq(sum, total) {
		t.Fatalf("per-key sum %v != workspace total %v", sum, total)
	}
}

// The visibility boundary, at the data layer. One user with two keys — one billing
// the company pool, one billing their own personal wallet. A team report must show
// only the company key; a personal report only the personal one.
func TestSpendByTokenWorkspaceIsolation(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws := seedEnterprise(t, d, "acme")
	uid := mkUser(t, d, "dual@acme.com")
	personalWS, err := d.PersonalWorkspaceID(ctx, uid)
	if err != nil {
		t.Fatalf("personal ws: %v", err)
	}
	if _, err := d.AddBalance(ctx, uid, TxKindAdjust, 100, "seed", "", true); err != nil {
		t.Fatalf("fund personal: %v", err)
	}

	corpKey, err := d.CreateUserToken(ctx, uid, TokenParams{Name: "corp", WorkspaceID: ws})
	if err != nil {
		t.Fatalf("corp key: %v", err)
	}
	homeKey, err := d.CreateUserToken(ctx, uid, TokenParams{Name: "home", WorkspaceID: personalWS})
	if err != nil {
		t.Fatalf("home key: %v", err)
	}

	charge(t, d, ws, uid, corpKey.ID, "claude-opus-4-8", 7.0)
	charge(t, d, personalWS, uid, homeKey.ID, "claude-fable-5", 2.0)

	team, err := d.SpendByToken(ctx, ReportFilter{Scope: WorkspaceScope(ws)})
	if err != nil {
		t.Fatalf("team report: %v", err)
	}
	if len(team) != 1 || team[0].TokenID != corpKey.ID {
		t.Fatalf("team report leaked personal keys: %+v", team)
	}
	if !approxEq(team[0].SpentUSD, 7.0) {
		t.Errorf("team spend = %v, want 7.0", team[0].SpentUSD)
	}

	home, err := d.SpendByToken(ctx, ReportFilter{Scope: WorkspaceScope(personalWS)})
	if err != nil {
		t.Fatalf("personal report: %v", err)
	}
	if len(home) != 1 || home[0].TokenID != homeKey.ID {
		t.Fatalf("personal report leaked corp keys: %+v", home)
	}
}

// An unscoped filter must return nothing, not everything. Failing open here would
// mean one forgetful caller silently dumps every tenant's ledger.
func TestUnscopedReportReturnsNothing(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws := seedEnterprise(t, d, "acme")
	uid := mkUser(t, d, "x@acme.com")
	k, _ := d.CreateUserToken(ctx, uid, TokenParams{Name: "k", WorkspaceID: ws})
	charge(t, d, ws, uid, k.ID, "m", 5)

	got, err := d.SpendByToken(ctx, ReportFilter{})
	if err != nil {
		t.Fatalf("SpendByToken: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unscoped report returned %d rows — it must fail closed", len(got))
	}
}

// Deleting a key leaves its ledger rows behind. They must still be reported, or
// the per-key breakdown stops adding up to the workspace total.
func TestSpendByTokenIncludesDeletedTokens(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws := seedEnterprise(t, d, "acme")
	uid := mkUser(t, d, "gone@acme.com")

	k, err := d.CreateUserToken(ctx, uid, TokenParams{Name: "doomed", WorkspaceID: ws})
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	charge(t, d, ws, uid, k.ID, "claude-opus-4-8", 4.0)
	if err := d.DeleteUserToken(ctx, k.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := d.SpendByToken(ctx, ReportFilter{Scope: WorkspaceScope(ws)})
	if err != nil {
		t.Fatalf("SpendByToken: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 (the orphaned spend of a deleted key)", len(got))
	}
	if !approxEq(got[0].SpentUSD, 4.0) {
		t.Errorf("orphaned spend = %v, want 4.0", got[0].SpentUSD)
	}
	if !got[0].Deleted {
		t.Error("row not flagged as belonging to a deleted key")
	}
}

// json_each over the tags column blows up on the ” default unless guarded. This
// workspace deliberately contains an untagged key alongside tagged ones — without
// the json_valid guard the whole query errors out.
func TestSpendByTagHandlesUntaggedKeys(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws := seedEnterprise(t, d, "acme")
	uid := mkUser(t, d, "tags@acme.com")

	k1, _ := d.CreateUserToken(ctx, uid, TokenParams{Name: "fe", WorkspaceID: ws, Tags: []string{"研发部", "前端"}})
	k2, _ := d.CreateUserToken(ctx, uid, TokenParams{Name: "be", WorkspaceID: ws, Tags: []string{"研发部"}})
	k3, _ := d.CreateUserToken(ctx, uid, TokenParams{Name: "none", WorkspaceID: ws}) // tags = ''

	charge(t, d, ws, uid, k1.ID, "m", 3.0)
	charge(t, d, ws, uid, k2.ID, "m", 2.0)
	charge(t, d, ws, uid, k3.ID, "m", 9.0)

	got, err := d.SpendByTag(ctx, ReportFilter{Scope: WorkspaceScope(ws)})
	if err != nil {
		t.Fatalf("SpendByTag (json_valid guard missing?): %v", err)
	}
	byTag := map[string]float64{}
	for _, s := range got {
		byTag[s.Tag] = s.SpentUSD
	}
	if !approxEq(byTag["研发部"], 5.0) {
		t.Errorf("研发部 = %v, want 5.0 (both tagged keys)", byTag["研发部"])
	}
	if !approxEq(byTag["前端"], 3.0) {
		t.Errorf("前端 = %v, want 3.0", byTag["前端"])
	}
	// The untagged key contributes to no label at all.
	if len(byTag) != 2 {
		t.Errorf("got labels %v, want exactly 研发部 + 前端", byTag)
	}

	// Filtering BY tag narrows the whole report.
	only, err := d.SpendByToken(ctx, ReportFilter{Scope: WorkspaceScope(ws), Tag: "前端"})
	if err != nil {
		t.Fatalf("tag-filtered SpendByToken: %v", err)
	}
	if len(only) != 1 || only[0].TokenID != k1.ID {
		t.Fatalf("tag filter returned %+v, want just the 前端 key", only)
	}
}

func TestListSpendRowsFilters(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws := seedEnterprise(t, d, "acme")
	uid := mkUser(t, d, "rows@acme.com")
	k1, _ := d.CreateUserToken(ctx, uid, TokenParams{Name: "a", WorkspaceID: ws})
	k2, _ := d.CreateUserToken(ctx, uid, TokenParams{Name: "b", WorkspaceID: ws})

	charge(t, d, ws, uid, k1.ID, "claude-opus-4-8", 1)
	charge(t, d, ws, uid, k2.ID, "gpt-5-codex", 2)
	charge(t, d, ws, uid, k1.ID, "gpt-5-codex", 3)

	all, total, err := d.ListSpendRows(ctx, ReportFilter{Scope: WorkspaceScope(ws)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("got %d rows (total %d), want 3", len(all), total)
	}

	byToken, total, err := d.ListSpendRows(ctx, ReportFilter{Scope: WorkspaceScope(ws), TokenID: k1.ID})
	if err != nil {
		t.Fatalf("by token: %v", err)
	}
	if total != 2 {
		t.Errorf("token filter total = %d, want 2", total)
	}
	for _, r := range byToken {
		if r.TokenID != k1.ID {
			t.Errorf("token filter leaked key %d", r.TokenID)
		}
	}

	byModel, total, err := d.ListSpendRows(ctx, ReportFilter{Scope: WorkspaceScope(ws), Model: "gpt-5-codex"})
	if err != nil {
		t.Fatalf("by model: %v", err)
	}
	if total != 2 {
		t.Errorf("model filter total = %d, want 2", total)
	}
	for _, r := range byModel {
		if r.Model != "gpt-5-codex" {
			t.Errorf("model filter leaked %q", r.Model)
		}
	}
}

// The window is half-open [from, to): a row created exactly at `to` belongs to the
// next window, not this one. Getting this wrong double-counts a row across two
// adjacent monthly exports.
func TestListSpendRowsTimeWindowIsHalfOpen(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws := seedEnterprise(t, d, "acme")
	uid := mkUser(t, d, "time@acme.com")
	k, _ := d.CreateUserToken(ctx, uid, TokenParams{Name: "k", WorkspaceID: ws})
	charge(t, d, ws, uid, k.ID, "m", 1)

	var ts int64
	if err := d.QueryRowContext(ctx, `SELECT created_at FROM wallet_tx WHERE kind='charge'`).Scan(&ts); err != nil {
		t.Fatalf("read ts: %v", err)
	}
	at := time.Unix(ts, 0)

	_, total, err := d.ListSpendRows(ctx, ReportFilter{Scope: WorkspaceScope(ws), From: at})
	if err != nil {
		t.Fatalf("from=at: %v", err)
	}
	if total != 1 {
		t.Errorf("from == created_at excluded the row (want inclusive lower bound)")
	}

	_, total, err = d.ListSpendRows(ctx, ReportFilter{Scope: WorkspaceScope(ws), To: at})
	if err != nil {
		t.Fatalf("to=at: %v", err)
	}
	if total != 0 {
		t.Errorf("to == created_at included the row (want exclusive upper bound)")
	}
}

func TestComputeStreaks(t *testing.T) {
	today := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	day := func(s string, spent float64) *DaySpend {
		return &DaySpend{Day: s, SpentUSD: spent, ChargeEvents: 1}
	}

	cases := []struct {
		name        string
		days        []*DaySpend
		wantCurrent int
		wantLongest int
	}{
		{"empty", nil, 0, 0},
		{"today only", []*DaySpend{day("2026-07-13", 1)}, 1, 1},
		{
			"run ending today",
			[]*DaySpend{day("2026-07-11", 1), day("2026-07-12", 1), day("2026-07-13", 1)},
			3, 3,
		},
		{
			// Mid-streak but hasn't used it yet today — the streak is alive.
			"run ending yesterday",
			[]*DaySpend{day("2026-07-11", 1), day("2026-07-12", 1)},
			2, 2,
		},
		{
			// Ended two days ago — history, not a streak.
			"run ended two days ago",
			[]*DaySpend{day("2026-07-10", 1), day("2026-07-11", 1)},
			0, 2,
		},
		{
			"gap splits runs, longest is historical",
			[]*DaySpend{
				day("2026-07-01", 1), day("2026-07-02", 1), day("2026-07-03", 1), day("2026-07-04", 1),
				day("2026-07-12", 1), day("2026-07-13", 1),
			},
			2, 4,
		},
		{
			// Zero-filled inactive days must not extend a streak.
			"zero-spend days are not active",
			[]*DaySpend{day("2026-07-11", 1), {Day: "2026-07-12"}, day("2026-07-13", 1)},
			1, 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur, longest := ComputeStreaks(tc.days, today)
			if cur != tc.wantCurrent || longest != tc.wantLongest {
				t.Fatalf("got (current=%d, longest=%d), want (%d, %d)",
					cur, longest, tc.wantCurrent, tc.wantLongest)
			}
		})
	}
}

func TestZeroFillDays(t *testing.T) {
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	sparse := []*DaySpend{{Day: "2026-07-11", SpentUSD: 2, ChargeEvents: 1}}

	got := ZeroFillDays(sparse, from, to)
	if len(got) != 4 {
		t.Fatalf("got %d days, want 4 (inclusive both ends)", len(got))
	}
	if got[0].Day != "2026-07-10" || got[0].SpentUSD != 0 {
		t.Errorf("first day = %+v, want empty 2026-07-10", got[0])
	}
	if got[1].Day != "2026-07-11" || !approxEq(got[1].SpentUSD, 2) {
		t.Errorf("populated day lost: %+v", got[1])
	}
	if got[3].Day != "2026-07-13" {
		t.Errorf("last day = %q, want 2026-07-13", got[3].Day)
	}
}
