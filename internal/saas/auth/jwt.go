package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID int64  `json:"uid"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// AudienceSession is written into the `aud` claim of every session token this
// issuer mints. Nothing verifies it yet, and that is deliberate: tokens issued
// before this field existed are still in flight (jwt_ttl defaults to 24h), and
// requiring a match at parse time would sign every current user out at deploy.
// Stamping it now is what makes a later rotation possible at all — without an
// audience there is no way to invalidate one class of token, or to tell tokens
// minted for this console apart from tokens minted for anything else. Tighten
// (jwt.WithAudience on Parse) only once no pre-audience token can still be
// valid, i.e. more than jwt_ttl after this ships.
const AudienceSession = "hypitoken-session"

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

func (i *Issuer) Issue(userID int64, role string) (string, time.Time, error) {
	exp := time.Now().Add(i.ttl)
	c := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{AudienceSession},
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	s, err := tok.SignedString(i.secret)
	return s, exp, err
}

func (i *Issuer) Parse(tokenStr string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("bad signing method")
		}
		return i.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return c, nil
}
