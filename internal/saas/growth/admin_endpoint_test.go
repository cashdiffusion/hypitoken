package growth_test

import (
	"context"
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/saas/growth"
)

// End-to-end over the three calls /admin/growth/analytics makes, against a DB
// carrying BOTH production corruptions at once. Two separate fixes were needed
// and the first only surfaced the second, because nothing exercised the whole
// endpoint in one go.
func TestAdminAnalyticsPathSurvivesProductionCorruption(t *testing.T) {
	store, svc := openTestDB(t)
	ctx := context.Background()

	if _, err := svc.CreateChannel(ctx, growth.ChannelParams{
		Slug: "prod", Name: "Prod", Enabled: true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := svc.RecordVisit(ctx, "prod", "ok-visitor", ""); err != nil {
		t.Fatalf("record visit: %v", err)
	}
	if _, err := store.ExecContext(ctx,
		`UPDATE channel_visits SET duration_ms = 5.06 WHERE visitor_id = 'ok-visitor'`); err != nil {
		t.Fatalf("seed REAL duration: %v", err)
	}
	if _, err := store.ExecContext(ctx,
		`INSERT INTO channel_visits (slug, visitor_id, first_seen, last_seen, duration_ms, created_at)
		 VALUES ('prod', 'broken', 'claudecode-mac', 0, 0, 0)`); err != nil {
		t.Fatalf("seed TEXT first_seen: %v", err)
	}

	if _, err := svc.Totals(ctx); err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if _, err := svc.Stats(ctx); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if _, err := svc.Timeseries(ctx, 14); err != nil {
		t.Fatalf("Timeseries: %v", err)
	}
}
