package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/relay"
)

func relayReq(t *testing.T, trusted bool, client, session, wireSession string) *gin.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if client != "" {
		req.Header.Set(relay.HeaderClient, client)
	}
	if session != "" {
		req.Header.Set(relay.HeaderSession, session)
	}
	if wireSession != "" {
		req.Header.Set("X-Claude-Code-Session-Id", wireSession)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	c.Set(relayTrustedKey, trusted)
	return c
}

// The security boundary: relay headers are self-asserted, so an untrusted
// caller sending them must change nothing. Otherwise anyone could mint
// scheduler slots at will and take a credential each.
func TestUntrustedCallerCannotDeclareIdentity(t *testing.T) {
	c := relayReq(t, false, relay.ClientID("victim"), "sess-x", "my-own-session")

	if _, ok := relayIdentity(c); ok {
		t.Fatal("relayIdentity honoured an untrusted caller")
	}
	if got := clientSlotID(c); got != "my-own-session" {
		t.Fatalf("slot = %q, want the caller's own session header", got)
	}

	// And with no session header of its own it falls back to the shared slot,
	// exactly as before this feature existed.
	plain := relayReq(t, false, relay.ClientID("victim"), "sess-x", "")
	if got := clientSlotID(plain); got != "" {
		t.Fatalf("slot = %q, want empty for an untrusted sessionless caller", got)
	}
}

// A trusted relay's declaration wins over the session header on the wire: that
// header describes the relay's hop, not the user behind it.
func TestTrustedRelayDeclarationWins(t *testing.T) {
	id := relay.ClientID("downstream-user")
	c := relayReq(t, true, id, "sess-1", "relay-own-session")

	got, ok := relayIdentity(c)
	if !ok {
		t.Fatal("relayIdentity rejected a trusted caller")
	}
	if got.Client != id || got.Session != "sess-1" {
		t.Fatalf("identity = %+v", got)
	}
	if slot := clientSlotID(c); slot != got.SlotID() || slot == "relay-own-session" {
		t.Fatalf("slot = %q, want the declared %q", slot, got.SlotID())
	}

	// Two users behind one relay token must not share a slot — the whole point.
	other := relayReq(t, true, relay.ClientID("another-user"), "sess-1", "relay-own-session")
	if clientSlotID(c) == clientSlotID(other) {
		t.Error("two downstream users collapsed onto one slot")
	}
}

// A trusted relay that sends no identity (an older build, or its own health
// check) must degrade to the pre-existing behaviour, not to a blank shared slot
// that every such request would share.
func TestTrustedRelayWithoutHeadersFallsBack(t *testing.T) {
	c := relayReq(t, true, "", "", "wire-session")
	if _, ok := relayIdentity(c); ok {
		t.Fatal("identity reported without a client id")
	}
	if got := clientSlotID(c); got != "wire-session" {
		t.Fatalf("slot = %q, want the wire session", got)
	}
}

func TestTrustedRelaySetMatching(t *testing.T) {
	set := newTrustedRelaySet([]string{
		"sk-plain-token",
		"sha256:" + hashToken("sk-hashed-token"),
		"   ", // ignored
	})
	if !set.Has("sk-plain-token") {
		t.Error("a raw entry did not match")
	}
	if !set.Has("sk-hashed-token") {
		t.Error("a sha256: entry did not match")
	}
	if set.Has("sk-someone-else") || set.Has("") {
		t.Error("matched a token that was never listed")
	}
	// The default must trust nobody.
	if newTrustedRelaySet(nil).Has("sk-plain-token") {
		t.Error("an empty config trusted a token")
	}
}

// Whatever we do with the identity, it must not travel further: forwarding it
// would hand an upstream vendor the shape of our client base.
func TestRelayHeadersAreNeverForwarded(t *testing.T) {
	h := http.Header{}
	h.Set(relay.HeaderClient, relay.ClientID("user"))
	h.Set(relay.HeaderSession, "sess")
	h.Set(relay.HeaderPeer, "cpa-claude/1.2.3")

	stripIngressHeaders(h)

	if _, ok := relay.Read(h); ok {
		t.Fatalf("relay identity survived into the upstream request: %v", h)
	}
}
