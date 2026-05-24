package kirocreds

import (
	"context"
	"fmt"
	"time"

	"github.com/wjsoj/cc-core/kiroauth"
)

// EnsureFresh refreshes the entry's access token if it has expired or will
// expire within skew. Mutates the store atomically. Returns the (possibly
// updated) Entry on success.
//
// Concurrent calls for the same id are NOT serialized — last writer wins —
// but the underlying file write is atomic per-entry. For high-concurrency
// production use, layer a per-id sync.Mutex in the caller.
func (s *Store) EnsureFresh(ctx context.Context, id string, skew time.Duration) (*Entry, error) {
	got, ok := s.Get(id)
	if !ok {
		return nil, fmt.Errorf("kirocreds: id %q not found", id)
	}
	if !got.Cred.IsExpired(skew) {
		return got, nil
	}
	if got.Cred.RefreshToken == "" {
		return nil, fmt.Errorf("kirocreds: id %q has no refresh token", id)
	}
	client := &kiroauth.Client{}
	tr, err := client.RefreshSocial(ctx, got.Cred.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("kirocreds: refresh: %w", err)
	}
	tr.ApplyTo(&got.Cred)
	if err := s.UpdateCredentials(id, got.Cred); err != nil {
		return nil, err
	}
	return got, nil
}
