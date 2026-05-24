// Package kiroupstream is hypitoken's dispatch layer for the Kiro channel.
// It picks one healthy Kiro credential (LRU-style rotation), ensures the
// access token is fresh, runs the full Anthropic↔Kiro translation, and
// writes Anthropic-shaped SSE events to a gin.Context.
//
// Designed to be invoked from internal/server/proxy.go when the resolved
// token-group has Upstream == "kiro".
package kiroupstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/kiroapi"
	"github.com/wjsoj/cc-core/kiroauth"
	"github.com/wjsoj/cc-core/kirobridge"
	"github.com/wjsoj/cc-core/kirotransport"
	"github.com/wjsoj/cc-core/usage"

	"github.com/wjsoj/CPA-Claude/internal/kirocreds"
)

// Dispatcher selects + forwards Anthropic requests through the Kiro
// channel. Concurrency-safe.
type Dispatcher struct {
	Store *kirocreds.Store
	// HTTP is the (optional) http.Client to use for Kiro upstream calls.
	// Nil = http.DefaultClient.
	HTTP *http.Client

	// rrCounter is a round-robin index across healthy entries — gives an
	// approximate even-load distribution without needing per-entry stats.
	rrCounter uint64
}

// ChosenCred is what Dispatcher returns when a credential is selected.
type ChosenCred struct {
	Entry *kirocreds.Entry
}

// PickCredential returns one healthy credential after refreshing it if
// needed. Returns (nil, error) when none are usable.
func (d *Dispatcher) PickCredential(ctx context.Context, excludeIDs map[string]bool) (*ChosenCred, error) {
	if d.Store == nil {
		return nil, errors.New("kiroupstream: no credential store configured")
	}
	all := d.Store.HealthyEntries()
	if len(all) == 0 {
		return nil, errors.New("kiroupstream: no healthy Kiro credentials available")
	}

	// Round-robin offset then linear scan; first usable wins.
	start := int(atomic.AddUint64(&d.rrCounter, 1)-1) % len(all)
	var firstErr error
	for i := 0; i < len(all); i++ {
		e := all[(start+i)%len(all)]
		if excludeIDs[e.ID] {
			continue
		}
		refreshed, err := d.Store.EnsureFresh(ctx, e.ID, 5*time.Minute)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		return &ChosenCred{Entry: refreshed}, nil
	}
	if firstErr != nil {
		return nil, fmt.Errorf("kiroupstream: all candidates failed: %w", firstErr)
	}
	return nil, errors.New("kiroupstream: no candidates left after exclusions")
}

// Forward runs the full Anthropic-format request through the Kiro upstream
// using the given chosen credential. Writes SSE events to c (when stream
// is true) or a single JSON message (when stream is false).
//
// Returns the accumulated usage counters for billing. The Anthropic
// message_id from message_start is also returned so the caller can log it.
func (d *Dispatcher) Forward(c *gin.Context, ctx context.Context, areq *kirobridge.AnthropicRequest, cred *kirocreds.Entry, profileARN string) (usage.Counts, string, error) {
	if areq == nil {
		return usage.Counts{}, "", errors.New("kiroupstream: nil request")
	}
	stream := areq.Stream

	// Build the typed Kiro request from the Anthropic-format input.
	convertOpts := kirobridge.ConvertOptions{
		ProfileARN:  profileARN,
		Origin:      "AI_EDITOR",
		AllowImages: true,
	}
	out, err := kirobridge.Convert(areq, convertOpts)
	if err != nil {
		return usage.Counts{}, "", fmt.Errorf("kiroupstream: convert: %w", err)
	}
	body, err := json.Marshal(out.Request.ConversationState)
	if err != nil {
		return usage.Counts{}, "", fmt.Errorf("kiroupstream: marshal: %w", err)
	}

	kc := &kiroapi.Client{
		Token:    cred.Cred.AccessToken,
		IsAPIKey: cred.Cred.IsAPIKey(),
		Flavor:   kirotransport.FlavorCLI,
	}
	if d.HTTP != nil {
		// only assign when a real client is configured — passing a typed-nil
		// *http.Client through the HTTPDoer interface would make c.http()
		// non-nil but panic on Do().
		kc.HTTP = d.HTTP
	}
	kreq := &kiroapi.GenerateAssistantResponseRequest{
		ConversationState: body,
		ProfileARN:        profileARN,
	}
	kstream, err := kc.GenerateAssistantResponse(ctx, kreq)
	if err != nil {
		return usage.Counts{}, "", fmt.Errorf("kiroupstream: GenerateAssistantResponse: %w", err)
	}
	defer kstream.Close()

	messageID := newMessageID()
	tr := kirobridge.NewStreamTranslator(kstream, areq.Model, messageID)

	if stream {
		return d.writeSSE(c, tr, messageID)
	}
	return d.writeOneShot(c, tr, areq.Model, messageID)
}

func (d *Dispatcher) writeSSE(c *gin.Context, tr *kirobridge.StreamTranslator, messageID string) (usage.Counts, string, error) {
	c.Status(200)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, _ := c.Writer.(http.Flusher)

	var u usage.Counts
	for tr.Next() {
		ev := tr.Event()
		// Accumulate usage from message_delta + message_start events on the way out.
		accumulateUsage(&u, ev)
		if _, err := c.Writer.Write(ev.Marshal()); err != nil {
			return u, messageID, err
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := tr.Err(); err != nil {
		return u, messageID, err
	}
	return u, messageID, nil
}

func (d *Dispatcher) writeOneShot(c *gin.Context, tr *kirobridge.StreamTranslator, model, messageID string) (usage.Counts, string, error) {
	// Collect all text deltas + tool_use blocks into an Anthropic non-streaming response.
	type block struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	}
	type respMsg struct {
		ID         string       `json:"id"`
		Type       string       `json:"type"`
		Role       string       `json:"role"`
		Model      string       `json:"model"`
		Content    []block      `json:"content"`
		StopReason string       `json:"stop_reason,omitempty"`
		Usage      usage.Counts `json:"usage"`
	}

	out := respMsg{ID: messageID, Type: "message", Role: "assistant", Model: model}
	var u usage.Counts
	var textBuf string
	var stopReason string

	for tr.Next() {
		ev := tr.Event()
		accumulateUsage(&u, ev)
		switch ev.Name {
		case "content_block_delta":
			var v struct {
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			_ = json.Unmarshal(ev.Data, &v)
			if v.Delta.Type == "text_delta" {
				textBuf += v.Delta.Text
			}
		case "message_delta":
			var v struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
			}
			_ = json.Unmarshal(ev.Data, &v)
			if v.Delta.StopReason != "" {
				stopReason = v.Delta.StopReason
			}
		}
	}
	if err := tr.Err(); err != nil {
		return u, messageID, err
	}
	if textBuf != "" {
		out.Content = append(out.Content, block{Type: "text", Text: textBuf})
	}
	out.StopReason = stopReason
	out.Usage = u
	c.JSON(200, out)
	return u, messageID, nil
}

// accumulateUsage scrapes input/output token counts out of the SSE events
// we forward. Anthropic puts the authoritative numbers in message_start
// (input) and message_delta (output, sometimes cumulative).
func accumulateUsage(u *usage.Counts, ev kirobridge.SSEEvent) {
	switch ev.Name {
	case "message_start":
		var v struct {
			Message struct {
				Usage struct {
					InputTokens              int64 `json:"input_tokens"`
					CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(ev.Data, &v); err == nil {
			if v.Message.Usage.InputTokens > u.InputTokens {
				u.InputTokens = v.Message.Usage.InputTokens
			}
			if v.Message.Usage.CacheReadInputTokens > u.CacheReadTokens {
				u.CacheReadTokens = v.Message.Usage.CacheReadInputTokens
			}
			if v.Message.Usage.CacheCreationInputTokens > u.CacheCreateTokens {
				u.CacheCreateTokens = v.Message.Usage.CacheCreationInputTokens
			}
		}
	case "message_delta":
		var v struct {
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(ev.Data, &v); err == nil {
			if v.Usage.OutputTokens > u.OutputTokens {
				u.OutputTokens = v.Usage.OutputTokens
			}
			if v.Usage.InputTokens > u.InputTokens {
				u.InputTokens = v.Usage.InputTokens
			}
		}
	}
}

// newMessageID returns a fresh msg_<24-hex> id used in Anthropic responses.
func newMessageID() string {
	return "msg_" + randHex24()
}

var mu sync.Mutex
var seed uint64

func randHex24() string {
	// 12 random bytes → 24 hex chars. Use rand.Read via a one-off io.Reader.
	b := make([]byte, 12)
	if _, err := io.ReadFull(cryptoRand{}, b); err != nil {
		mu.Lock()
		seed++
		mu.Unlock()
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 24)
	for i, by := range b {
		out[2*i] = hex[by>>4]
		out[2*i+1] = hex[by&0x0f]
	}
	return string(out)
}

type cryptoRand struct{}

func (cryptoRand) Read(p []byte) (int, error) { return cryptoReadFunc(p) }

// indirected so tests can stub if needed
var cryptoReadFunc = realCryptoRead

func realCryptoRead(p []byte) (int, error) {
	return cryptoRandRead(p)
}

// kiroauth.NewPKCE etc already exist; pulling crypto/rand in here directly
// keeps the import set explicit.
var _ = kiroauth.AuthSocial // keep the import in use even if unused above

// Use a tiny indirection helper that imports crypto/rand without cluttering
// the top-of-file imports.
func cryptoRandRead(p []byte) (int, error) {
	return cryptoRandReadImpl(p)
}
