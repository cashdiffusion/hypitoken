package kirocreds

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	ccauth "github.com/wjsoj/cc-core/auth"
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
	ProxyURL    string // optional outbound proxy for token exchange + future refresh
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
// they authorize — typically http://localhost:3128. proxyURL (optional) is the
// outbound proxy used for /oauth/token and persisted onto the credential so
// later refreshes route the same way.
//
// label is stored on the session so Finish can apply it to the new entry.
func (p *PKCESessions) Start(redirectURI, label, proxyURL string) (signInURL, state string, err error) {
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
		ProxyURL:    strings.TrimSpace(proxyURL),
	}
	return kiroauth.SignInURL(pkce, redirectURI), pkce.State, nil
}

// Finish takes the callback parameters (code+state), looks up the matching
// session, calls Kiro's /oauth/token through the session's proxy (if any),
// returns the resulting credentials with ProxyURL stamped on. On success the
// session is consumed (deleted).
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

	// Kiro echoes back redirect_uri with the /oauth/callback path appended —
	// not the bare base URL the SignInURL uses. Verified against the captured
	// real-CLI flow in crack/kiro/login/docs/04: body sends
	// "http://localhost:3128/oauth/callback?login_option=github".
	echo := strings.TrimRight(s.RedirectURI, "/") + "/oauth/callback"
	if loginOption != "" {
		echo += "?login_option=" + loginOption
	}
	client := &kiroauth.Client{HTTP: httpClientFor(s.ProxyURL)}
	tr, err := client.ExchangeCode(ctx, code, s.Verifier, echo)
	if err != nil {
		return kiroauth.Credentials{}, "", fmt.Errorf("kirocreds: exchange: %w", err)
	}
	cred := kiroauth.Credentials{AuthMethod: kiroauth.AuthSocial, ProxyURL: s.ProxyURL}
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

// ParseKiroCallback extracts code+state+login_option from any of: the full
// browser redirect URL (`http://localhost:3128/oauth/callback?code=...&state=...&login_option=github`),
// a bare query string (`code=...&state=...&login_option=...`), or a `#`
// fallback (`code#state#login_option`).
func ParseKiroCallback(input string) (code, state, loginOption string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", "", errors.New("empty callback")
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		u, e := url.Parse(input)
		if e != nil {
			return "", "", "", e
		}
		q := u.Query()
		if oe := q.Get("error"); oe != "" {
			return "", "", "", fmt.Errorf("oauth error: %s", oe)
		}
		return q.Get("code"), q.Get("state"), q.Get("login_option"), nil
	}
	if strings.Contains(input, "=") {
		if vals, e := url.ParseQuery(strings.TrimPrefix(input, "?")); e == nil {
			return vals.Get("code"), vals.Get("state"), vals.Get("login_option"), nil
		}
	}
	if strings.Contains(input, "#") {
		parts := strings.SplitN(input, "#", 3)
		if len(parts) == 3 {
			return parts[0], parts[1], parts[2], nil
		}
		if len(parts) == 2 {
			return parts[0], parts[1], "", nil
		}
	}
	return "", "", "", fmt.Errorf("kirocreds: unable to parse callback %q", input)
}

// httpClientFor returns an *http.Client routed through proxyURL (empty = direct).
// Kiro endpoints don't fingerprint-check, so plain transport is fine.
func httpClientFor(proxyURL string) *http.Client {
	if strings.TrimSpace(proxyURL) == "" {
		return http.DefaultClient
	}
	return ccauth.NewPlainHTTPClient(proxyURL, false)
}
