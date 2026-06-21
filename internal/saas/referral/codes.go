package referral

import (
	"context"
	"crypto/rand"
	"regexp"
	"strings"
)

// inviteAlphabet is the charset for invite codes: lowercase letters + digits
// minus visually-ambiguous characters (0/o, 1/l/i). The code doubles as a ?ref=
// value, so it stays within the growth module's URL-safe slug charset
// (^[a-z0-9][a-z0-9_-]{0,30}$) and can flow through the existing attribution
// capture unchanged.
const inviteAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// redeemAlphabet is the charset for gift redeem codes: uppercase + digits minus
// ambiguous characters, formatted in groups for readability on the card back.
const redeemAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// inviteCodeRe mirrors growth.NormalizeSlug so a personal code resolves through
// the same ?ref= path. Used to validate a candidate before a DB lookup.
var inviteCodeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,30}$`)

func randString(alphabet string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out), nil
}

// normalizeInviteCode lowercases/trims a candidate invite code and returns it
// only if it is shape-valid; "" signals reject.
func normalizeInviteCode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !inviteCodeRe.MatchString(s) {
		return ""
	}
	return s
}

// normalizeRedeemCode canonicalises a gift redeem code for lookup: uppercased,
// trimmed, with separators stripped so "hypi-4f2k-9j3m" and "HYPI4F2K9J3M" match
// the stored "HYPI4F2K9J3M".
func normalizeRedeemCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", " ", "").Replace(s)
}

// codeTaken reports whether a code already exists in the given table.
func (s *Service) codeTaken(ctx context.Context, table, code string) bool {
	var x int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE code = ? LIMIT 1`, code).Scan(&x)
	return err == nil // a row was found
}

// newInviteCode mints a unique invite code, retrying on the (rare) collision.
func (s *Service) newInviteCode(ctx context.Context) (string, error) {
	for range 6 {
		code, err := randString(inviteAlphabet, 8)
		if err != nil {
			return "", err
		}
		if !s.codeTaken(ctx, "referral_cards", code) {
			return code, nil
		}
	}
	// Extremely unlikely; widen the space as a fallback.
	return randString(inviteAlphabet, 12)
}

// newRedeemCode mints a unique gift redeem code (stored compact, shown grouped).
func (s *Service) newRedeemCode(ctx context.Context) (string, error) {
	for range 6 {
		body, err := randString(redeemAlphabet, 8)
		if err != nil {
			return "", err
		}
		if code := "HYPI" + body; !s.codeTaken(ctx, "gift_cards", code) {
			return code, nil
		}
	}
	body, err := randString(redeemAlphabet, 12)
	if err != nil {
		return "", err
	}
	return "HYPI" + body, nil
}

// formatRedeem renders a stored redeem code grouped for display (HYPI-4F2K-9J3M).
func formatRedeem(code string) string {
	if !strings.HasPrefix(code, "HYPI") || len(code) < 5 {
		return code
	}
	body := code[4:]
	var parts []string
	parts = append(parts, "HYPI")
	for i := 0; i < len(body); i += 4 {
		end := min(i+4, len(body))
		parts = append(parts, body[i:end])
	}
	return strings.Join(parts, "-")
}
