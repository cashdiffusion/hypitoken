package server

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/relay"
)

// Trusted-relay ingress. CPA-Claude can route here through a single API key,
// which without this makes every one of its users look like one client: the
// pool keys stickiness on (provider, client token, session), so they would all
// pin to one credential while the rest of the fleet sits idle.
//
// A relay stamps cc-core/relay headers naming the downstream caller. We believe
// them only from a token the operator listed in trusted_relay_tokens, and use
// them ONLY to pick the scheduler slot — limits, quota and billing stay on the
// relay's own token, because the relay is one paying customer however many users
// sit behind it, and because a limit keyed on a self-asserted header is a limit
// anyone can evade by inventing a new value.

// relayTrustedKey is the gin context key set by clientAuth for a caller the
// operator has designated a trusted relay.
const relayTrustedKey = "trusted_relay"

// trustedRelaySet resolves the configured entries once at startup. Entries are
// stored hashed either way, so a raw token in the config never sits in memory
// as a comparable secret and the lookup is constant-time in shape.
type trustedRelaySet map[string]struct{}

func newTrustedRelaySet(entries []string) trustedRelaySet {
	if len(entries) == 0 {
		return nil
	}
	set := make(trustedRelaySet, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if h, ok := strings.CutPrefix(e, "sha256:"); ok {
			set[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
			continue
		}
		set[hashToken(e)] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// Has reports whether this client token is a trusted relay.
func (s trustedRelaySet) Has(clientToken string) bool {
	if len(s) == 0 || clientToken == "" {
		return false
	}
	_, ok := s[hashToken(clientToken)]
	return ok
}

// relayIsTrusted reports whether clientAuth designated this request a trusted
// relay. Anything reading a relay header must gate on this.
func relayIsTrusted(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, _ := c.Get(relayTrustedKey)
	trusted, _ := v.(bool)
	return trusted
}

// relayIdentity returns the downstream caller a trusted relay declared.
// ok is false for every untrusted caller, whatever headers it sent.
func relayIdentity(c *gin.Context) (relay.Identity, bool) {
	if !relayIsTrusted(c) {
		return relay.Identity{}, false
	}
	return relay.Read(c.Request.Header)
}
