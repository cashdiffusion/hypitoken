package kirocreds

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wjsoj/cc-core/kiroauth"
)

// PKCESession holds the in-flight state for one admin-initiated Kiro login.
//
// The admin UI calls StartLogin → gets a sign-in URL + state token; the user
// opens the URL in a browser, authorizes, browser hits the redirect_uri,
// admin UI POSTs the code+state back via FinishLogin.
type PKCESession struct {
	State       string
	Verifier    string
	RedirectURI string
	CreatedAt   time.Time
	Label       string // user-friendly label to attach to the new entry
}

// PKCESessions is an in-memory registry of pending logins. Sessions expire
// after pkceTTL (5 min) which matches a reasonable browser-tab attention span.
type PKCESessions struct {
	mu       sync.Mutex
	sessions map[string]*PKCESession
}

const pkceTTL = 5 * time.Minute

// NewPKCESessions returns an empty registry.
func NewPKCESessions() *PKCESessions { return &PKCESessions{sessions: make(map[string]*PKCESession)} }

// Start generates a fresh PKCE pair, registers it, and returns the sign-in URL
// + state token. redirectURI is the URL the user's browser will land on after
// they authorize — typically the admin panel's own callback endpoint, e.g.
// https://your-host/admin/api/kiro/oauth-callback.
//
// label is stored on the session so FinishLogin can apply it to the new entry.
func (p *PKCESessions) Start(redirectURI, label string) (signInURL, state string, err error) {
	pkce, err := kiroauth.NewPKCE()
	if err != nil {
		return "", "", fmt.Errorf("kirocreds: NewPKCE: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gcLocked()
	p.sessions[pkce.State] = &PKCESession{
		State:       pkce.State,
		Verifier:    pkce.Verifier,
		RedirectURI: redirectURI,
		CreatedAt:   time.Now(),
		Label:       label,
	}
	return kiroauth.SignInURL(pkce, redirectURI), pkce.State, nil
}

// Finish takes the callback parameters (code+state), looks up the matching
// session, calls Kiro's /oauth/token, returns the resulting credentials.
// On success the session is consumed (deleted).
//
// Caller is responsible for storing the credentials via Store.Add.
//
// The loginOption argument is the value of `?login_option=` that Kiro's
// browser side appends to the redirect_uri — it MUST be echoed back to
// /oauth/token verbatim or token exchange fails.
func (p *PKCESessions) Finish(ctx context.Context, code, state, loginOption string) (kiroauth.Credentials, string, error) {
	p.mu.Lock()
	s, ok := p.sessions[state]
	if ok {
		delete(p.sessions, state)
	}
	p.mu.Unlock()
	if !ok {
		return kiroauth.Credentials{}, "", errors.New("kirocreds: PKCE state not found or expired")
	}

	echo := s.RedirectURI
	if loginOption != "" {
		// Kiro appends ?login_option=… to the redirect_uri before token
		// exchange. If the redirect already has a query, append with &.
		if containsQuery(echo) {
			echo += "&login_option=" + loginOption
		} else {
			echo += "?login_option=" + loginOption
		}
	}
	client := &kiroauth.Client{}
	tr, err := client.ExchangeCode(ctx, code, s.Verifier, echo)
	if err != nil {
		return kiroauth.Credentials{}, "", fmt.Errorf("kirocreds: exchange: %w", err)
	}
	cred := kiroauth.Credentials{AuthMethod: kiroauth.AuthSocial}
	tr.ApplyTo(&cred)
	return cred, s.Label, nil
}

func (p *PKCESessions) gcLocked() {
	cutoff := time.Now().Add(-pkceTTL)
	for k, s := range p.sessions {
		if s.CreatedAt.Before(cutoff) {
			delete(p.sessions, k)
		}
	}
}

func containsQuery(u string) bool {
	for i := 0; i < len(u); i++ {
		if u[i] == '?' {
			return true
		}
	}
	return false
}
