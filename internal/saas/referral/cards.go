package referral

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Card is a user-minted invite card: a customisable face plus a unique code
// that doubles as the ?ref= value on the share link.
type Card struct {
	ID         int64     `json:"id"`
	OwnerID    int64     `json:"owner_user_id"`
	CampaignID int64     `json:"campaign_id"`
	Code       string    `json:"code"`
	CardStyle  string    `json:"card_style"`
	CardTone   string    `json:"card_tone"`
	Tagline    string    `json:"tagline"`
	Message    string    `json:"message"`
	Imprints   int64     `json:"impressions"`
	CreatedAt  time.Time `json:"created_at"`
}

const cardCols = `id, owner_user_id, campaign_id, code, card_style, card_tone, tagline, message, impressions, created_at`

func scanCard(row interface{ Scan(...any) error }) (*Card, error) {
	var c Card
	var created int64
	if err := row.Scan(&c.ID, &c.OwnerID, &c.CampaignID, &c.Code, &c.CardStyle, &c.CardTone, &c.Tagline, &c.Message, &c.Imprints, &created); err != nil {
		return nil, err
	}
	c.CreatedAt = time.Unix(created, 0)
	return &c, nil
}

func normalizeStyle(s string) string {
	if s == "openai" {
		return "openai"
	}
	return "claude"
}

func normalizeTone(t string) string {
	if t == "light" {
		return "light"
	}
	return "dark"
}

// runeLimit truncates a user-supplied string to n runes (so multibyte CJK
// taglines / messages can't blow past a sane card-face length).
func runeLimit(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// MintCard creates a new invite card for a user under the active campaign.
func (s *Service) MintCard(ctx context.Context, ownerID int64, style, tone, tagline, message string) (*Card, error) {
	code, err := s.newInviteCode(ctx)
	if err != nil {
		return nil, err
	}
	var campaignID int64
	if camp, cerr := s.ActiveCampaign(ctx); cerr == nil {
		campaignID = camp.ID
	}
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO referral_cards
		(owner_user_id, campaign_id, code, card_style, card_tone, tagline, message, impressions, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		ownerID, campaignID, code, normalizeStyle(style), normalizeTone(tone), runeLimit(tagline, 60), runeLimit(message, 140), now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return scanCard(s.DB.QueryRowContext(ctx, `SELECT `+cardCols+` FROM referral_cards WHERE id = ?`, id))
}

// UpdateCard mutates a card's face. Only the owner may update (enforced at the
// handler). Code is immutable.
func (s *Service) UpdateCard(ctx context.Context, ownerID, cardID int64, style, tone, tagline, message string) (*Card, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE referral_cards SET card_style=?, card_tone=?, tagline=?, message=? WHERE id=? AND owner_user_id=?`,
		normalizeStyle(style), normalizeTone(tone), runeLimit(tagline, 60), runeLimit(message, 140), cardID, ownerID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return scanCard(s.DB.QueryRowContext(ctx, `SELECT `+cardCols+` FROM referral_cards WHERE id = ?`, cardID))
}

// ListCards returns a user's invite cards, newest first.
func (s *Service) ListCards(ctx context.Context, ownerID int64) ([]*Card, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+cardCols+` FROM referral_cards WHERE owner_user_id = ? ORDER BY id DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PrimaryCard returns a user's invite card, minting a default one on first call
// so every user always has a shareable link.
func (s *Service) PrimaryCard(ctx context.Context, ownerID int64) (*Card, error) {
	c, err := scanCard(s.DB.QueryRowContext(ctx, `SELECT `+cardCols+` FROM referral_cards WHERE owner_user_id = ? ORDER BY id ASC LIMIT 1`, ownerID))
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return s.MintCard(ctx, ownerID, "claude", "dark", "", "")
}

// cardByCode resolves an invite code to its card (the inviter). Returns
// ErrNotFound when the code isn't a personal invite code.
func (s *Service) cardByCode(ctx context.Context, code string) (*Card, error) {
	c, err := scanCard(s.DB.QueryRowContext(ctx, `SELECT `+cardCols+` FROM referral_cards WHERE code = ?`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// TouchImpression bumps a card's share-impression counter (best-effort).
func (s *Service) TouchImpression(ctx context.Context, code string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE referral_cards SET impressions = impressions + 1 WHERE code = ?`, code)
}

// InviteURL builds the absolute share link for a code (SiteURL + /?ref=code).
func (s *Service) InviteURL(code string) string {
	base := strings.TrimRight(s.SiteURL, "/")
	return base + "/?ref=" + code
}
