package arena

import (
	"fmt"
	"hash/fnv"
)

// arenaPseudonyms is the anonymous-display name pool. Users who have NOT opted
// into the public leaderboard appear under one of these (stable per user via a
// FNV-1a hash of their id), mirroring the /status page's anonymisation so we
// never leak a real identity for someone who didn't opt in.
var arenaPseudonyms = []string{
	"Aurora", "Blaze", "Comet", "Delta", "Echo", "Flux", "Glyph", "Helix",
	"Ion", "Jet", "Kilo", "Lumen", "Mecha", "Nova", "Orbit", "Pixel",
	"Quark", "Relay", "Spark", "Turbo", "Umbra", "Vertex", "Wisp", "Xenon",
	"Yonder", "Zephyr", "Atlas", "Byte", "Cipher", "Drift", "Ember", "Forge",
}

// fnv32 is the stable hash used for both the actor id and the pseudonym pick.
func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// actorIDFor returns an opaque, stable token grouping all activity for one
// user. Safe to expose to the client (it is not the real user id) so the
// office renderer can map repeated events to the same character.
func actorIDFor(userID int64) string {
	return fmt.Sprintf("a%08x", fnv32(fmt.Sprintf("hypi-arena-actor:%d", userID)))
}

// pseudonymFor returns the stable anonymous display name for a user.
func pseudonymFor(userID int64) string {
	h := fnv32(fmt.Sprintf("hypi-arena-nym:%d", userID))
	return arenaPseudonyms[int(h)%len(arenaPseudonyms)]
}

// displayName resolves the public-facing name: the real nickname when the user
// opted in, otherwise the stable pseudonym.
func displayName(userID int64, nickname string, optIn bool) string {
	if optIn && nickname != "" {
		return nickname
	}
	return pseudonymFor(userID)
}
