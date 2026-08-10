package db

import (
	"context"
	"testing"
	"time"
)

// TestSpendBreakdownMatchesIndividualQueries is the safety net for the merged
// single-pass rollup: whatever SpendBreakdown returns must be indistinguishable
// from calling the five original reports, which are the tested, shipped
// behaviour. It exists because SpendBreakdown reproduces in Go what SQL was
// doing (grouping, ordering, the deleted-key and unattributed-bucket edge
// cases), and a silent divergence there is a billing report that lies.
func TestSpendBreakdownMatchesIndividualQueries(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws := seedEnterprise(t, d, "acme")
	alice := mkUser(t, d, "alice@acme.com")
	bob := mkUser(t, d, "bob@acme.com")

	ka, err := d.CreateUserToken(ctx, alice, TokenParams{
		Name: "alice-laptop", WorkspaceID: ws, Tags: []string{"rnd", "frontend"},
	})
	if err != nil {
		t.Fatalf("key a: %v", err)
	}
	kb, err := d.CreateUserToken(ctx, bob, TokenParams{
		Name: "bob-ci", WorkspaceID: ws, Tags: []string{"rnd"},
	})
	if err != nil {
		t.Fatalf("key b: %v", err)
	}
	// An untagged key, to prove the tag fold skips it instead of erroring.
	kc, err := d.CreateUserToken(ctx, bob, TokenParams{Name: "bob-scratch", WorkspaceID: ws})
	if err != nil {
		t.Fatalf("key c: %v", err)
	}
	// A key that gets deleted afterwards: its ledger rows survive and must show
	// up under by_token as Deleted, but contribute to no tag.
	kd, err := d.CreateUserToken(ctx, bob, TokenParams{
		Name: "bob-gone", WorkspaceID: ws, Tags: []string{"legacy"},
	})
	if err != nil {
		t.Fatalf("key d: %v", err)
	}

	charge(t, d, ws, alice, ka.ID, "claude-opus-4-8", 3.0)
	charge(t, d, ws, alice, ka.ID, "claude-opus-4-8", 2.25)
	charge(t, d, ws, alice, ka.ID, "claude-haiku-4-5", 0.5)
	charge(t, d, ws, bob, kb.ID, "gpt-5-codex", 1.5)
	charge(t, d, ws, bob, kc.ID, "gpt-5-codex", 0.75)
	charge(t, d, ws, bob, kd.ID, "claude-opus-4-8", 4.0)
	// token_id 0 = the pre-v15 unattributed bucket.
	charge(t, d, ws, bob, 0, "", 0.125)

	if err := d.DeleteUserToken(ctx, kd.ID); err != nil {
		t.Fatalf("delete key d: %v", err)
	}

	for _, f := range []ReportFilter{
		{Scope: WorkspaceScope(ws)},
		{Scope: UserScope(alice)},
		{Scope: UserScope(bob)},
		{Scope: WorkspaceScope(ws), From: time.Now().AddDate(0, 0, -7), To: time.Now().AddDate(0, 0, 1)},
		{Scope: WorkspaceScope(ws), Model: "claude-opus-4-8"},
		{Scope: WorkspaceScope(ws), TokenID: ka.ID},
		{Scope: WorkspaceScope(ws), Tag: "rnd"},
	} {
		assertBreakdownMatches(t, d, f)
	}
}

func assertBreakdownMatches(t *testing.T, d *DB, f ReportFilter) {
	t.Helper()
	ctx := context.Background()

	got, err := d.SpendBreakdown(ctx, f)
	if err != nil {
		t.Fatalf("SpendBreakdown(%+v): %v", f.Scope, err)
	}

	wantTotal, err := d.SpendSummary(ctx, f)
	if err != nil {
		t.Fatalf("SpendSummary: %v", err)
	}
	if !approxEq(got.Total.SpentUSD, wantTotal.SpentUSD) ||
		got.Total.ChargeEvents != wantTotal.ChargeEvents ||
		got.Total.ActiveTokens != wantTotal.ActiveTokens ||
		got.Total.InputTokens != wantTotal.InputTokens ||
		got.Total.OutputTokens != wantTotal.OutputTokens ||
		got.Total.CacheReadTokens != wantTotal.CacheReadTokens ||
		got.Total.CacheCreateTokens != wantTotal.CacheCreateTokens {
		t.Errorf("total mismatch\n got %+v\nwant %+v", *got.Total, *wantTotal)
	}

	// Rows are matched by their grouping key, not by position. The reference
	// queries order by SUM(...) DESC and leave exact ties in whatever order
	// SQLite happens to emit; SpendBreakdown breaks ties deterministically so
	// the dashboard doesn't reshuffle between loads. Comparing positionally
	// would therefore fail on a tie even though both agree on every number, so
	// each list is checked for the same members and, separately, for being
	// correctly ordered.
	wantTokens, err := d.SpendByToken(ctx, f)
	if err != nil {
		t.Fatalf("SpendByToken: %v", err)
	}
	if len(got.ByToken) != len(wantTokens) {
		t.Fatalf("by_token length %d, want %d", len(got.ByToken), len(wantTokens))
	}
	gotTokens := map[int64]*TokenSpend{}
	for _, g := range got.ByToken {
		gotTokens[g.TokenID] = g
	}
	assertDescending(t, "by_token", len(got.ByToken), func(i int) float64 { return got.ByToken[i].SpentUSD })
	for i := range wantTokens {
		w := wantTokens[i]
		g, ok := gotTokens[w.TokenID]
		if !ok {
			t.Errorf("by_token missing token %d", w.TokenID)
			continue
		}
		if g.TokenID != w.TokenID || g.Name != w.Name || g.Email != w.Email ||
			g.UserID != w.UserID || g.Deleted != w.Deleted ||
			!approxEq(g.SpentUSD, w.SpentUSD) || g.ChargeEvents != w.ChargeEvents ||
			g.InputTokens != w.InputTokens || g.OutputTokens != w.OutputTokens ||
			g.CacheReadTokens != w.CacheReadTokens || g.CacheCreateTokens != w.CacheCreateTokens ||
			!g.FirstAt.Equal(w.FirstAt) || !g.LastAt.Equal(w.LastAt) {
			t.Errorf("by_token[%d] mismatch\n got %+v\nwant %+v", i, *g, *w)
		}
		if len(g.Tags) != len(w.Tags) {
			t.Errorf("by_token[%d] tags %v, want %v", i, g.Tags, w.Tags)
			continue
		}
		for j := range w.Tags {
			if g.Tags[j] != w.Tags[j] {
				t.Errorf("by_token[%d] tags %v, want %v", i, g.Tags, w.Tags)
				break
			}
		}
	}

	wantModels, err := d.SpendByModel(ctx, f)
	if err != nil {
		t.Fatalf("SpendByModel: %v", err)
	}
	if len(got.ByModel) != len(wantModels) {
		t.Fatalf("by_model length %d, want %d", len(got.ByModel), len(wantModels))
	}
	gotModels := map[string]*ModelSpend{}
	for _, g := range got.ByModel {
		gotModels[g.Model] = g
	}
	assertDescending(t, "by_model", len(got.ByModel), func(i int) float64 { return got.ByModel[i].SpentUSD })
	for i := range wantModels {
		w := wantModels[i]
		g, ok := gotModels[w.Model]
		if !ok {
			t.Errorf("by_model missing model %q", w.Model)
			continue
		}
		if g.Model != w.Model || !approxEq(g.SpentUSD, w.SpentUSD) ||
			g.ChargeEvents != w.ChargeEvents || g.InputTokens != w.InputTokens ||
			g.OutputTokens != w.OutputTokens || g.CacheReadTokens != w.CacheReadTokens ||
			g.CacheCreateTokens != w.CacheCreateTokens {
			t.Errorf("by_model[%d] mismatch\n got %+v\nwant %+v", i, *g, *w)
		}
	}

	wantDays, err := d.SpendByDay(ctx, f)
	if err != nil {
		t.Fatalf("SpendByDay: %v", err)
	}
	if len(got.ByDay) != len(wantDays) {
		t.Fatalf("by_day length %d, want %d", len(got.ByDay), len(wantDays))
	}
	for i := range wantDays {
		g, w := got.ByDay[i], wantDays[i]
		if g.Day != w.Day || !approxEq(g.SpentUSD, w.SpentUSD) || g.ChargeEvents != w.ChargeEvents {
			t.Errorf("by_day[%d] mismatch\n got %+v\nwant %+v", i, *g, *w)
		}
	}

	wantTags, err := d.SpendByTag(ctx, f)
	if err != nil {
		t.Fatalf("SpendByTag: %v", err)
	}
	if len(got.ByTag) != len(wantTags) {
		t.Fatalf("by_tag length %d, want %d\n got %+v", len(got.ByTag), len(wantTags), dumpTags(got.ByTag))
	}
	gotTags := map[string]*TagSpend{}
	for _, g := range got.ByTag {
		gotTags[g.Tag] = g
	}
	assertDescending(t, "by_tag", len(got.ByTag), func(i int) float64 { return got.ByTag[i].SpentUSD })
	for i := range wantTags {
		w := wantTags[i]
		g, ok := gotTags[w.Tag]
		if !ok {
			t.Errorf("by_tag missing tag %q", w.Tag)
			continue
		}
		if g.Tag != w.Tag || !approxEq(g.SpentUSD, w.SpentUSD) ||
			g.ChargeEvents != w.ChargeEvents || g.Tokens != w.Tokens {
			t.Errorf("by_tag[%d] mismatch\n got %+v\nwant %+v", i, *g, *w)
		}
	}
}

func dumpTags(ts []*TagSpend) []TagSpend {
	out := make([]TagSpend, 0, len(ts))
	for _, t := range ts {
		out = append(out, *t)
	}
	return out
}

// assertDescending checks a breakdown is ordered biggest-spender-first.
func assertDescending(t *testing.T, name string, n int, at func(int) float64) {
	t.Helper()
	for i := 1; i < n; i++ {
		if at(i) > at(i-1)+1e-9 {
			t.Errorf("%s not descending at %d: %v then %v", name, i, at(i-1), at(i))
		}
	}
}
