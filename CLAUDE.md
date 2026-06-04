# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

CPA-Claude is a Go reverse-proxy that fans client requests across multiple upstream Anthropic / OpenAI credentials (OAuth + API keys) on **two independent HTTP endpoints** — Claude on `:8317` (`/v1/messages`), Codex on `:8318` (`/v1/chat/completions`, `/v1/responses`). Pool, client tokens, usage ledger, pricing, request log, and admin panel are shared; the credential subset and the body shaping differ per provider.

Derivative of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (MIT). The Anthropic OAuth refresh, Codex JWT parsing, and uTLS Chrome transport were lifted from upstream.

## Build & run

```bash
make build              # build admin SPA (bun) + Go binary into bin/hypitoken
make web-dev            # Vite dev server with API proxy to :8317 (frontend hot reload)
make tidy               # go mod tidy
make lint               # all linters: golangci-lint (Go) + Biome (admin SPA)
make lint-go            # golangci-lint run ./...   (config: .golangci.yml, v2 schema)
make lint-web           # Biome check over internal/admin/web/src
make fmt                # auto-format: golangci-lint fmt (Go) + Biome write (web)
go build ./...          # Go-only build (skips SPA; admin panel falls back to embedded /dist)
go test ./...           # all tests
go test ./internal/server/... -timeout 30s -v -run TestBootstrap   # single test/group
```

**Linting is a CI gate (blocking).** `.github/workflows/ci.yml` runs `lint-go` (golangci-lint v2) and `lint-web` (Biome) as separate required jobs alongside `build`. Keep both green:
- **Go** — `golangci-lint` **v2** (`.golangci.yml` is v2-schema; the local binary must be v2.x — v1 cannot parse a go1.25 module). Intentional exceptions live as documented `exclusions.rules` in `.golangci.yml` (Z-Pay MD5, sidecar timing `math/rand`, Stripe `client_secret`, the deliberately-unwired Datadog sidecar) or per-line `//nolint:<linter> // reason`.
- **Web** — **Biome** does both lint + format (replaces ESLint/Prettier), config at `internal/admin/web/biome.json`. Run via `bun run lint` / `bun run lint:fix` / `bun run format`. Strict `recommended` ruleset, zero warnings allowed. `catch` blocks use `errMsg`/`errStatus` from `@/lib/utils` (no `e: any`); shared API shapes live in `@/lib/types`. Hook-dependency exceptions (e.g. intentional `refreshTick` refresh triggers) use a single-line `// biome-ignore lint/correctness/useExhaustiveDependencies: reason` directly above the dependency array.

The admin SPA at `internal/admin/web/` (Preact + Vite + Tailwind, managed with **bun, not npm**) is built into `internal/admin/web/dist/` and embedded via `//go:embed` in `internal/admin/admin.go`. The `//go:generate` directive there runs `bun install --frozen-lockfile && bun run build`. CI (`.github/workflows/ci.yml`) calls `make web` before `go build`, so SPA is mandatory in releases.

`make build` requires `bun` on PATH. Plain `go build ./...` works without bun if `internal/admin/web/dist/` already contains a build (or the embedded asset can be empty for backend-only iteration).

## Architecture (the parts that span files)

### Endpoint × provider matrix

`internal/server/server.go` constructs **N gin engines, one per enabled endpoint**. Each engine is bound to one provider (`auth.ProviderAnthropic` or `auth.ProviderOpenAI`) and serves only the routes that make sense for it. The "primary" endpoint (Claude if enabled, else Codex) additionally hosts the admin panel + public `/status`.

The shared pieces (`auth.Pool`, `usage.Store`, `clienttoken.Store`, `pricing.Catalog`, `requestlog.Writer`) live on `Server` and are injected into both engines. The split-by-engine matters because: per-provider stickiness, per-provider concurrency budgets, and per-provider RPM limits all key on `(provider | clientToken)` — Claude saturation must NOT block a client's Codex traffic.

### Credential pool & sticky sessions

`internal/auth/pool.go` is the credential scheduler. `Pool.Acquire(ctx, provider, clientToken, group, model, exclude...)` picks an OAuth credential by:

1. Sticky reuse — if `clientToken` already has a healthy assignment for this provider, return it.
2. Fewest-active-sessions among healthy OAuth in the matching group with spare `max_concurrent`.
3. API-key fallback when every OAuth is saturated/quota-exceeded/dead.

A "session" in pool semantics is one `(provider, client_token, sessionID)` slot observed within `ActiveWindow` (default 10 min), where `sessionID` is the client's per-window identifier — `clientSlotID` in `proxy.go` reads it from `X-Claude-Code-Session-Id` (Claude Code) or `Session_id` (Codex CLI), falling back to `""` (one slot per token) for raw API callers. This means one user opening N CLI windows presents N independent sessions and can be load-balanced across N different credentials. Per-token RPM and concurrency caps deliberately stay keyed on the token alone, so opening more windows doesn't multiply a client's rate budget. `Pool.Acquire`/`Release`/`Unstick` all take the `sessionID` and must be passed the same value across the request. `Pool.Release` is called once per request to keep the active counter accurate. `Pool.Unstick` breaks the assignment when an upstream error suggests the credential is bad. `Pool.ReportUpstreamError` translates 401/403/429 into the right combination of cooldown / hard-failure / stealth-ban detection — this logic is shared by both Anthropic and Codex paths and changes here ripple everywhere.

Health states are kept on `auth.Auth` itself: `MarkSuccess` / `MarkFailure` / `MarkHardFailure` / `MarkRateLimited` / `MarkUsageLimitReached` / `MarkClientCancel`. Hard failures are sticky (manual clear from admin) except for one daily reset job (`Pool.RunDailyAnthropicAPIKeyReset`) that wipes API-key hard-failures so a transient overnight outage doesn't pin them dead forever.

### Anthropic forward path (`internal/server/proxy.go`)

`forward()` is the per-request loop: budget pre-check → RPM gate → concurrency gate → up to 4 retries on different credentials. Per attempt, `doForward` (OAuth) or `doForwardAnthropicAPIKey` (API key) actually talks to upstream.

The OAuth path applies **two layers of mimicry** to look like a real Claude Code 2.1.126 client:

- **Header layer** — `applyAnthropicHeaders` in `proxy.go` sets pinned `User-Agent`, `X-Stainless-*`, `Anthropic-Beta`, `X-App`, `X-Claude-Code-Session-Id`, `X-Client-Request-Id`. Constants live in `internal/server/fingerprint.go` (`CLICurrentVersion`, `claudeCLIUserAgent`, `claudeAnthropicBetaFull`, etc.). Whenever you bump the CC version target, **all of these need to move together** or the version in the User-Agent will disagree with the `cc_version=` baked into the body's billing block, which is itself a fingerprint signal.

- **Body layer** — `applyClaudeCodeBodyMimicry` in `mimicry.go` rewrites system into the canonical 4-block CC layout `[billing, "You are Claude Code...", ...originalSystem-with-cache_control]`, sets `metadata.user_id` to the JSON `{device_id, account_uuid, session_id}` shape, signs `cch=<xxhash5>` of the final body. The client's original prompt is preserved verbatim — only the surrounding wrapper is normalized. **Skipped entirely for Haiku models** (Anthropic doesn't third-party-check Haiku) and for requests whose system already starts with the CC prompt prefix (real CLI passing through).

`maybeDecompressResponse` in `proxy.go` transparently un-gzips/un-brs upstream responses because we advertise `Accept-Encoding: gzip, br` to match real CC, but every internal path (usage parsing, SSE streamer, model rewrite) wants plain bytes.

### SimIdentity — the per-account fingerprint anchor

`SimIdentity{ AccountKey, AccountUUID, ClientToken }` (defined in `mimicry.go`) is the central handle that ties together every identity-bearing field:

- `DeviceIDFor(AccountKey)` — sha256-anchored, **identical for all requests routed through the same OAuth account** (across credential file rotations, across multiple client tokens). Mimics the real CC `machine-id sha256` value.
- `SessionIDFor(id, body)` — derived from `(account, clientToken, sha256(first user message))` so multi-turn conversations keep one session_id but a new conversation rotates. Same function powers both the body's `metadata.user_id.session_id` and the `X-Claude-Code-Session-Id` header so they stay consistent.
- `AccountKey()` on `auth.Auth` falls back through `AccountUUID > Email > ID`. Existing credentials from before the `account_uuid` field was added still work via the email fallback; new logins capture the real UUID from the OAuth token-exchange response.

**Invariant:** for one OAuth account routed by N downstream client tokens, upstream sees one device with N concurrent CC sessions — exactly what one user opening multiple `claude` windows would look like. Don't change this without re-checking every place that derives identity.

### Sidecar (auxiliary traffic emulation) — `internal/server/sidecar.go`

`sidecarMgr.Notify(a, clientToken)` is called from `doForward` after credential acquisition. First-touch of a `(account, clientToken)` pair starts three goroutines:

1. **`runBootstrap`** — 9 sidecar requests in real CC's captured timing (T+0 GrowthBook, T+0.16 oauth/account/settings, T+0.16 grove, T+1.25 bootstrap, T+1.25 penguin, T+1.27 quota probe, T+1.95 mcp-registry, T+1.95 v1/mcp_servers, T+2.38 downloads/releases). Each step has its own `User-Agent` (Bun / axios / claude-code / claude-cli — real CC mixes 4 client identities), its own `Anthropic-Beta` (mcp_servers uses `mcp-servers-2025-12-04`, quota probe uses a 5-item short list, etc), and `noAuth: true` for the public CDN.
2. **`runHeartbeat`** — POSTs `/api/event_logging/v2/batch` every ~18s ±40% with a `tengu_dir_search` ClaudeCodeInternalEvent. Starts after T+8s.
3. **`runDatadogHeartbeat`** — POSTs `https://http-intake.logs.us5.datadoghq.com/api/v2/logs` every ~25s ±40% with the same event in Datadog's flatter shape. Starts after T+14s. Uses `DD-API-KEY: pubea5604404508cdd34afb69e6f42a05bc` (verified global across two independent capture sessions; re-check on each major CC release in case of rotation).

A `bootstrapSessionID` is shared by all three streams (matches real CC, where bootstrap + quota probe + event_logging all carry the same session UUID). Distinct from the per-conversation chat session_id from `SessionIDFor`.

GC (`gcLoop`) evicts virtual sessions idle > 30 min; heartbeats also self-stop after 5 min idle. `Server.Shutdown` cancels every live session's context.

API-key credentials never trigger sidecars (the third-party-detection signal we're hiding only applies to OAuth subscription accounts).

### Codex path (`codex_proxy.go` + `codex_oauth_proxy.go`)

OpenAI-format requests on the Codex endpoint. **API-key credentials** forward to `api.openai.com` mostly verbatim; **OAuth (ChatGPT Plus/Pro/Team)** credentials forward to `chatgpt.com/backend-api/codex/responses` with the session/account headers the real Codex CLI sends. JWT parsing in `internal/auth/codex_jwt.go` extracts `chatgpt_account_id` and `chatgpt_plan_type` from the id_token; `codex_models.go` synthesizes a per-plan-tier `/v1/models` catalog so clients see only what their subscription allows.

> **Codex OAuth has not been smoke-tested against a real ChatGPT subscription token in production.** The auth-layer paths (token exchange, refresh, JWT) work; full request/response parity against `chatgpt.com/backend-api` is pending. If you change anything in this path, exercise both the API-key and OAuth branches.

### Capture archive — `crack/`

Contains complete recorded sessions of real Claude Code 2.1.126 traffic, used as ground truth for every fingerprint constant in the codebase.

- `crack/raw/<mode>-session-full.json` and `crack/login/raw/login-session-full.json` — original Whistle dumps.
- `crack/oauth/`, `crack/apikey/`, `crack/login/` — three parallel modes; each has `rows/` (per-request decoded JSON) and `docs/` (per-request markdown write-ups). `crack/login/` covers the OAuth PKCE login flow specifically (12 requests).
- `crack/COMPARE.md` — OAuth-vs-APIkey diff. `crack/login/README.md` — PKCE flow + CPA alignment table.
- `crack/scripts/{split,sanitize,gen}.py` — all helper scripts live here, separate from data. `split.py <mode>` decodes raw dumps into per-row JSON, `sanitize.py` does idempotent in-place redaction across the whole tree, `gen.py <mode>` re-renders markdown docs from rows. See `crack/scripts/README.md`.

**When bumping the CC version target, re-capture and update `crack/` first**, then update fingerprint constants to match. Pipeline: `split.py → sanitize.py → gen.py → sanitize.py` (the trailing sanitize is critical — `gen.py` may reproduce raw values from rows into docs).

## Conventions worth knowing

- **bun, not npm** — every JS toolchain invocation in this repo uses bun. `npx` will technically work but the lockfile is `bun.lock`.
- **All identity derivation is content-addressed**, no random UUIDs except `X-Client-Request-Id` and the internal `event_id` field. If you need a new stable identifier, derive it from `accountKey` (or `accountKey + clientToken` if it should differ across downstream users).
- **OAuth credential file fields are append-only** — `parseFile` in `internal/auth/oauth.go` tolerates missing fields with sensible fallbacks; new fields go through the `_ = raw["new_field"].(...)` pattern so old credential files keep loading.
- **Per-provider stickiness uses `auth.NormalizeProvider(provider) + "|" + clientToken`** as the key — Claude and Codex share a token but not a slot. Don't collapse this.
- **Hop-by-hop headers + ingress headers are stripped before forwarding** (`hopHeaders` map and `stripIngressHeaders` in `proxy.go`). This is critical when behind Cloudflare Tunnel — `Cdn-Loop: cloudflare` triggers CF's loop-prevention WAF on `api.anthropic.com`. Don't loosen this filter.
- **Tests** — `internal/server/sidecar_test.go` runs against a live `httptest.Server` and exercises real timing (~10s wall clock for the bootstrap suite). When adding sidecar steps, the test asserts each endpoint hits with the right `User-Agent` and `Anthropic-Beta` — extend the `wants` map.
