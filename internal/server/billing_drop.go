package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// What a dropped charge leaves behind, and what keeps a retried one from
// landing twice.
//
// Every forward path settles the wallet after the upstream response has been
// written, and every one of them handles a failed Charge the same way: log a
// warning and move on, because the customer has already been served and the
// only thing left to protect is the ledger. Two things were wrong with how the
// row was then recorded. billed_usd kept the official price — the value the
// path had pencilled in before asking the wallet — so every revenue report
// summed money nobody was debited (about $2k of it in August 2026). And the
// only trace of the drop was user_id == 0, a shape shared with legacy tokens
// and usage-less rows, so finding the drops meant inferring them. billingDropped
// is the explicit marker; the sites zero billed_usd alongside it.

// billingDropped is the request-log Error marker for a turn whose wallet
// charge could not be written. The row still carries cost_usd, so
// reconcile-charges can price it later; this is what makes it findable
// without reading user_id as a tell.
func billingDropped(err error) string {
	if err == nil {
		return ""
	}
	return "billing dropped: " + truncate([]byte(err.Error()), 160)
}

// joinLogError adds a second condition to a request-log Error field without
// losing the first — a truncated stream whose charge also failed to write
// needs both facts on the row.
func joinLogError(existing, add string) string {
	switch {
	case add == "":
		return existing
	case existing == "":
		return add
	default:
		return existing + " | " + add
	}
}

// chargeKeyCtxKey carries the wallet idempotency key on a charge context.
type chargeKeyCtxKey struct{}

// chargeBaseIDKey is the gin key under which a request's charge id is memoised,
// so every charge slot of one request (main, advisor sub-calls, WS turns)
// derives from the same base.
const chargeBaseIDKey = "charge_base_id"

// ChargeIdemKey returns the idempotency key attached by chargeCtx /
// chargeCtxSlot, or "" for a context that carries none. Exported for the SaaS
// adapter, which lives in another package; an empty key means the caller must
// take the non-idempotent path.
func ChargeIdemKey(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(chargeKeyCtxKey{}).(string)
	return v
}

// WithChargeIdemKey attaches an explicit idempotency key to ctx. The proxy
// derives its keys through chargeCtx / chargeCtxSlot; this is for callers that
// already own a durable id for the movement (a service job, a test).
func WithChargeIdemKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, chargeKeyCtxKey{}, key)
}

// chargeBaseID returns this request's charge id, minting it on first use.
// Random rather than derived: two identical requests from one client are two
// charges, and nothing about the request body may be allowed to collide them.
func chargeBaseID(c *gin.Context) string {
	if v, ok := c.Get(chargeBaseIDKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(b[:])
	c.Set(chargeBaseIDKey, id)
	return id
}
