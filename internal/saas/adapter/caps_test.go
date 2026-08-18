package adapter

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/server"
)

func TestPreCheckPerTokenCapsIgnoreSiblingTokenSpend(t *testing.T) {
	tests := []struct {
		name     string
		infoCap  func(*server.SaaSTokenInfo)
		wantCode string
	}{
		{
			name: "daily",
			infoCap: func(info *server.SaaSTokenInfo) {
				info.DailyUSDCap = 5
			},
			wantCode: "api_key_daily_limit_exceeded",
		},
		{
			name: "monthly",
			infoCap: func(info *server.SaaSTokenInfo) {
				info.MonthlyUSDCap = 5
			},
			wantCode: "api_key_monthly_limit_exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := db.Open(filepath.Join(t.TempDir(), "caps.db"))
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })

			ctx := context.Background()
			user, err := store.CreateUser(ctx, tt.name+"@example.com", "hash", db.RoleUser, 1, true)
			if err != nil {
				t.Fatalf("create user: %v", err)
			}
			sibling, err := store.CreateUserToken(ctx, user.ID, db.TokenParams{Name: "sibling"})
			if err != nil {
				t.Fatalf("create sibling token: %v", err)
			}
			subject, err := store.CreateUserToken(ctx, user.ID, db.TokenParams{Name: "subject"})
			if err != nil {
				t.Fatalf("create subject token: %v", err)
			}

			charge := func(tokenID int64) {
				t.Helper()
				if _, _, err := store.ChargeWorkspaceWithFloor(
					ctx, user.PersonalWorkspaceID, user.ID, db.TxKindCharge, 10,
					"cap-test", "", 0, db.ChargeMeta{TokenID: tokenID},
				); err != nil {
					t.Fatalf("charge token %d: %v", tokenID, err)
				}
			}

			adapter := &Adapter{DB: store}
			info := server.SaaSTokenInfo{
				TokenID:    subject.ID,
				UserID:     user.ID,
				BalanceUSD: 100,
			}
			tt.infoCap(&info)

			charge(sibling.ID)
			charge(0) // unattributed legacy spend must not land on a real key
			if capErr := adapter.PreCheck(ctx, info); capErr != nil {
				t.Fatalf("sibling token spend consumed subject cap: %+v", capErr)
			}

			charge(subject.ID)
			capErr := adapter.PreCheck(ctx, info)
			if capErr == nil {
				// The return is redundant at runtime — t.Fatal ends the
				// goroutine — but staticcheck only recognises that for a
				// t.Fatal in the test function's own body, not inside a
				// t.Run closure, and flags every field access below as a
				// possible nil dereference (SA5011) without it.
				t.Fatal("subject token spend did not trigger its cap")
				return
			}
			if capErr.Code != tt.wantCode {
				t.Fatalf("error code = %q, want %q", capErr.Code, tt.wantCode)
			}
			if capErr.Status != http.StatusTooManyRequests {
				t.Fatalf("error status = %d, want %d", capErr.Status, http.StatusTooManyRequests)
			}
			if got := capErr.Details["spent_usd"]; got != float64(10) {
				t.Fatalf("spent_usd = %v, want 10", got)
			}
			if got := capErr.Details["limit_usd"]; got != float64(5) {
				t.Fatalf("limit_usd = %v, want 5", got)
			}
		})
	}
}
