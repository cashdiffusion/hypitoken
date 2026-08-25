package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// LegacyAdminSSO builds the hook main.go installs as admin.SSOAuth: it turns a
// SaaS session JWT into an authorization decision for the legacy
// /admin/api/* console.
//
// The question it answers is "does the store say this user is an operator",
// never "does this token say so". A valid signature only proves the holder had
// saas.jwt_secret; it says nothing about whether the uid exists, is still
// enabled, or still holds the role. The secret is shared with the sibling
// HypiHub gateway, so copies of it live in that project's backups, CI secrets
// and developer machines. An earlier version of this hook returned
// claims.Role == "admin" without touching the database, which meant anyone
// holding such a copy could mint {uid: <anything>, role: "admin"} — the uid did
// not even have to name a real user — and drive every mutation the legacy
// console exposes: deleting upstream credentials, patching or resetting client
// tokens, and so on.
//
// So this is deliberately the same trust model as RequireUser: verify the
// signature, then load the row and believe the row.
//
// The returned hook reports (allowed, isAdmin, err):
//   - (false, false, nil) — no bearer token, bad signature, unknown uid, or a
//     disabled account. The caller answers 401.
//   - (false, false, err) — the store could not answer. The caller must fail
//     closed (503); falling back to the claims here would turn a database
//     hiccup into a privilege escalation.
//   - (true, isAdmin, nil) — the uid resolved to a live user, and isAdmin is
//     the role stored against that row.
//
// There is no cache in front of the lookup, matching RequireUser: every request
// on this path is a console call, the read is a primary-key hit on SQLite, and
// any cache would put a window between "operator disabled/demoted" and "loses
// access" — exactly the property this function exists to guarantee.
func LegacyAdminSSO(iss *Issuer, store *db.DB) func(c *gin.Context) (bool, bool, error) {
	return func(c *gin.Context) (bool, bool, error) {
		if iss == nil || store == nil {
			return false, false, errors.New("sso bridge: not configured")
		}
		raw := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
			return false, false, nil
		}
		claims, err := iss.Parse(strings.TrimSpace(raw[len("bearer "):]))
		if err != nil {
			// A token we cannot verify is an unauthenticated caller, not a
			// server-side failure: answer 401, don't fail closed with 503.
			return false, false, nil //nolint:nilerr // bad signature is a 401, not an error to propagate
		}
		u, err := store.GetUser(c.Request.Context(), claims.UserID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				// A signed token naming a uid that has no row. Nothing to
				// authorize against, so it is simply not a valid session.
				return false, false, nil
			}
			return false, false, fmt.Errorf("sso bridge: load user %d: %w", claims.UserID, err)
		}
		if u.Disabled {
			return false, false, nil
		}
		return true, u.Role == db.RoleAdmin, nil
	}
}
