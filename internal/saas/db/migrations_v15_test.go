package db

import (
	"context"
	"testing"
)

// The v15 backfill rewrites historical charge rows in place, on live money data,
// exactly once. There is no second chance and no undo, so it gets the most
// paranoid test in the package: every ref shape production has ever written, plus
// the malformed shapes that must NOT be guessed at.
//
// A fresh test DB is already at v15, so the ALTERs have run and the (empty)
// backfill has fired. We insert v14-era rows — ref populated, token_id/model at
// their defaults — and re-run the exact same statement the migration uses.
func TestV15BackfillTokenAttribution(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "backfill@example.com")

	cases := []struct {
		name      string
		kind      string
		ref       string
		wantToken int64
		wantModel string
	}{
		{"typical", TxKindCharge, "token=97 model=claude-opus-4-8", 97, "claude-opus-4-8"},
		{"single digit", TxKindCharge, "token=1 model=x", 1, "x"},
		{"multi digit", TxKindCharge, "token=12345678 model=gpt-5-codex", 12345678, "gpt-5-codex"},
		{"empty model", TxKindCharge, "token=5 model=", 5, ""},
		{"model with spaces", TxKindCharge, "token=5 model=some model with spaces", 5, "some model with spaces"},

		// Everything below must be left ALONE. Attributing money to the wrong
		// key is strictly worse than attributing it to nobody, so a ref that
		// doesn't parse cleanly stays in the token_id=0 "unattributed" bucket.
		{"topup out_trade_no", TxKindTopup, "2026071312345", 0, ""},
		{"adjust bonus", TxKindAdjust, "bonus", 0, ""},
		{"non-numeric id", TxKindCharge, "token=abc model=x", 0, ""},
		{"partially numeric id", TxKindCharge, "token=9x model=x", 0, ""},
		{"no separator", TxKindCharge, "token=9", 0, ""},
		{"empty id", TxKindCharge, "token= model=x", 0, ""},
		{"unrelated charge ref", TxKindCharge, "manual-correction", 0, ""},
	}

	ids := make([]int64, len(cases))
	for i, tc := range cases {
		res, err := d.ExecContext(ctx,
			`INSERT INTO wallet_tx (user_id, workspace_id, kind, amount_usd, ref, note, created_at) VALUES (?, 1, ?, -1.0, ?, '', 0)`,
			uid, tc.kind, tc.ref)
		if err != nil {
			t.Fatalf("seed %q: %v", tc.name, err)
		}
		ids[i], _ = res.LastInsertId()
	}

	// The statement under test is the migration's, verbatim.
	if _, err := d.ExecContext(ctx, backfillTokenAttributionSQL); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	for i, tc := range cases {
		var gotToken int64
		var gotModel string
		if err := d.QueryRowContext(ctx, `SELECT token_id, model FROM wallet_tx WHERE id = ?`, ids[i]).
			Scan(&gotToken, &gotModel); err != nil {
			t.Fatalf("read back %q: %v", tc.name, err)
		}
		if gotToken != tc.wantToken || gotModel != tc.wantModel {
			t.Errorf("%s (ref=%q): got (token=%d, model=%q), want (token=%d, model=%q)",
				tc.name, tc.ref, gotToken, gotModel, tc.wantToken, tc.wantModel)
		}
	}
}

// The backfill must be safe to run twice — it's idempotent by construction (the
// second pass re-derives the same values from the untouched ref), and a botched
// deploy that re-applies it must not corrupt anything.
func TestV15BackfillIsIdempotent(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "idem@example.com")

	if _, err := d.ExecContext(ctx,
		`INSERT INTO wallet_tx (user_id, workspace_id, kind, amount_usd, ref, note, created_at) VALUES (?, 1, ?, -1.0, 'token=42 model=claude-fable-5', '', 0)`,
		uid, TxKindCharge); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i := range 3 {
		if _, err := d.ExecContext(ctx, backfillTokenAttributionSQL); err != nil {
			t.Fatalf("backfill pass %d: %v", i, err)
		}
	}
	var tokenID int64
	var model string
	if err := d.QueryRowContext(ctx, `SELECT token_id, model FROM wallet_tx WHERE ref = 'token=42 model=claude-fable-5'`).
		Scan(&tokenID, &model); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if tokenID != 42 || model != "claude-fable-5" {
		t.Fatalf("got (%d, %q), want (42, %q)", tokenID, model, "claude-fable-5")
	}
}

// user_tokens.tags defaults to ” (not '[]'), matching the groups column. That
// makes json_each(tags) throw "malformed JSON" unless every query guards it —
// this pins the default so the guard in usage_reports.go stays necessary and
// correct.
func TestV15TagsDefaultsToEmptyString(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "tags@example.com")

	tok, err := d.CreateUserToken(ctx, uid, TokenParams{Name: "untagged"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	var tags string
	if err := d.QueryRowContext(ctx, `SELECT tags FROM user_tokens WHERE id = ?`, tok.ID).Scan(&tags); err != nil {
		t.Fatalf("read tags: %v", err)
	}
	if tags != "" {
		t.Fatalf("tags default = %q, want empty string", tags)
	}
}
