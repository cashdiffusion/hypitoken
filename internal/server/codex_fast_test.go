package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gorillaws "github.com/gorilla/websocket"
	"github.com/wjsoj/cc-core/auth"
)

// Drives the real HTTP forwarder, including sanitize, upstream observation,
// token extraction and settlement. OAuth's default is deliberately different
// from an API-key downgrade. Cache tokens must get the same tier adjustment.
func TestCodexFastHTTPBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, oauth := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, tier := range []string{"", "fast", "priority"} {
				for _, observed := range []string{"", "default", "priority", "flex"} {
					t.Run(fmt.Sprintf("oauth=%t/stream=%t/%s/%s", oauth, stream, tier, observed), func(t *testing.T) {
						seen := make(chan string, 1)
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							body, _ := io.ReadAll(r.Body)
							var req struct {
								Tier string `json:"service_tier"`
							}
							if err := json.Unmarshal(body, &req); err != nil {
								t.Error(err)
							}
							seen <- req.Tier
							response := fmt.Sprintf(`{"id":"resp_fast","object":"response","status":"completed","output":[],"service_tier":%q,"usage":{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":400}}}`, observed)
							if oauth || stream {
								w.Header().Set("Content-Type", "text/event-stream")
								fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
							} else {
								w.Header().Set("Content-Type", "application/json")
								_, _ = io.WriteString(w, response)
							}
						}))
						defer upstream.Close()
						cred := &auth.Auth{ID: "fast-account", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI, AccessToken: "test-token", ExpiresAt: time.Now().Add(time.Hour)}
						if !oauth {
							cred.Kind = auth.KindAPIKey
							cred.BaseURL = upstream.URL
						}
						s := codexHTTPTestServer(upstream.URL, cred)
						s.cfg.OpenAIBaseURL = upstream.URL
						body := []byte(fmt.Sprintf(`{"model":"gpt-5.5","input":[{"role":"user","content":"hello"}],"stream":%t,"service_tier":%q}`, stream, tier))
						w := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(w)
						c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(string(body)))
						retry, done := s.doForwardCodex(c, cred, "/v1/responses", body, stream, "gpt-5.5", "fast-test-token", "tester", "", time.Now(), 1)
						if retry || !done || w.Code != 200 {
							t.Fatalf("retry=%t done=%t status=%d body=%s", retry, done, w.Code, w.Body.String())
						}
						wantTier := tier
						if tier == "fast" {
							wantTier = "priority"
						}
						if got := <-seen; got != wantTier {
							t.Fatalf("wire tier %q want %q", got, wantTier)
						}
						ratio := 1.0
						if tier != "" {
							ratio = 2.5
						}
						if observed == "flex" {
							ratio = 0.5
						} else if observed == "default" && !oauth {
							ratio = 1
						}
						want := 0.0092 * ratio
						if got := s.usage.WeeklyCostUSD("fast-test-token"); math.Abs(got-want) > 1e-12 {
							t.Fatalf("cost %.8f want %.8f", got, want)
						}
					})
				}
			}
		}
	}
}

// Two turns use different models and tiers on ONE connection. Closing the
// handler drains the async billing queue before the final ledger assertion.
func TestCodexFastWSBillingPerTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	seen := make(chan string, 2)
	upgrader := gorillaws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-codex-routing-hint"); got != "model=gpt-5.5;tier=priority" {
			t.Errorf("routing hint %q", got)
		}
		up, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer up.Close()
		for i := 0; i < 2; i++ {
			_, frame, err := up.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var req struct {
				Tier string `json:"service_tier"`
			}
			if err := json.Unmarshal(frame, &req); err != nil {
				t.Error(err)
				return
			}
			seen <- req.Tier
			response := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","status":"completed","service_tier":"default","usage":{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":400}}}}`, i)
			if err := up.WriteMessage(gorillaws.TextMessage, []byte(response)); err != nil {
				t.Error(err)
				return
			}
		}
		// Keep the backend alive until the test client closes, so both terminal
		// events reach the downstream leg before the pump's stop closes sockets.
		_, _, _ = up.ReadMessage()
	}))
	defer backend.Close()
	cred := &auth.Auth{ID: "fast-ws-account", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI, AccessToken: "test-token", ExpiresAt: time.Now().Add(time.Hour)}
	s := codexHTTPTestServer(backend.URL, cred)
	s.cfg.CodexWS.Enabled = true
	s.cfg.CodexWS.ReadLimitBytes = 1 << 20
	finished := make(chan struct{})
	router := gin.New()
	router.GET("/v1/responses", func(c *gin.Context) {
		defer close(finished)
		c.Set("client_token", "fast-ws-token")
		c.Set("client_name", "tester")
		s.handleCodexResponsesWS(c)
	})
	front := httptest.NewServer(router)
	defer front.Close()
	client, resp, err := gorillaws.DefaultDialer.Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/v1/responses", nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for i, frame := range []string{
		`{"type":"response.create","model":"gpt-5.5","input":[],"service_tier":"fast"}`,
		`{"type":"response.create","model":"gpt-6-astra","input":[]}`,
	} {
		if err := client.WriteMessage(gorillaws.TextMessage, []byte(frame)); err != nil {
			t.Fatal(err)
		}
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, out, err := client.ReadMessage()
		if err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		if !strings.Contains(string(out), "response.completed") {
			t.Fatalf("turn %d: %s", i, out)
		}
		want := ""
		if i == 0 {
			want = "priority"
		}
		if got := <-seen; got != want {
			t.Fatalf("turn %d wire tier %q want %q", i, got, want)
		}
	}
	_ = client.Close()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("billing queue did not drain")
	}
	// First: (600*5 + 200*30 + 400*.5)/1M *2.5 = .023.
	// Second: (600*10 + 200*50 + 400*1)/1M = .0164.
	if got := s.usage.WeeklyCostUSD("fast-ws-token"); math.Abs(got-.0394) > 1e-12 {
		t.Fatalf("two-turn cost %.8f want .0394", got)
	}
}

type fastFinalRead struct{ body []byte }

func (r *fastFinalRead) Read(p []byte) (int, error) {
	n := copy(p, r.body)
	r.body = r.body[n:]
	if len(r.body) == 0 {
		return n, io.EOF
	}
	return n, nil
}
func TestCodexFinalReadFramesBeforeEOF(t *testing.T) {
	r := newLineReader(&fastFinalRead{body: []byte("event: response.completed\ndata: {}\n\ntail")})
	for _, want := range []string{"event: response.completed\n", "data: {}\n", "\n"} {
		got, err := r.readLine()
		if string(got) != want || err != nil {
			t.Fatalf("got %q/%v want %q", got, err, want)
		}
	}
	got, err := r.readLine()
	if string(got) != "tail" || !errors.Is(err, io.EOF) {
		t.Fatalf("tail %q/%v", got, err)
	}
}
