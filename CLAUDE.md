# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

hypitoken (Go module still named `CPA-Claude`, binary `bin/hypitoken`) is a Go reverse-proxy that fans client requests across multiple upstream Anthropic / OpenAI / Kiro credentials (OAuth + API keys) on **two proxy endpoints** — Claude on `:8317` (`/v1/messages`), Codex on `:8318` (`/v1/chat/completions`, `/v1/responses`) — plus an optional **发卡网 (shop)** listener on `:8319`. On top of the raw proxy sit two optional product layers: a **SaaS multi-tenant billing layer** (`/api/v2/*`, user accounts + USD wallet) and the embedded **admin/landing/console SPA**.

The reusable proxy core (credential pool, usage ledger, pricing, client tokens, request log, rate limiting, the CC mimicry/sidecar fingerprint, thinking-signature handling, and the whole Kiro bridge) lives in the external module **`github.com/wjsoj/cc-core`**. This repo is the application layer that wires those pieces into endpoints and adds SaaS, shop, Kiro credential management, and the admin panel. **CPA-Claude (`/home/wjs/Documents/project/Go/CPA-Claude`) is a sibling fork that consumes the same cc-core** — fingerprint/mimicry changes usually need to land in cc-core (which both consume) plus hypitoken's own vendored copy (see below).

Derivative of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (MIT). The Anthropic OAuth refresh, Codex JWT parsing, and uTLS Chrome transport originated upstream and now live in cc-core.

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
go test ./internal/server/... -timeout 60s -run TestBootstrap   # sidecar suite (~23s live timing)
```

**Linting is a CI gate (blocking).** `.github/workflows/ci.yml` runs `lint-go` (golangci-lint v2) and `lint-web` (Biome) as separate required jobs alongside `build`. Keep both green:
- **Go** — `golangci-lint` **v2** (`.golangci.yml` is v2-schema; the local binary must be v2.x — v1 cannot parse a go1.25 module). Intentional exceptions live as documented `exclusions.rules` in `.golangci.yml` (Z-Pay MD5, sidecar timing `math/rand`, Stripe `client_secret`, the deliberately-unwired Datadog sidecar) or per-line `//nolint:<linter> // reason`.
- **Web** — **Biome** does both lint + format (replaces ESLint/Prettier), config at `internal/admin/web/biome.json`. Run via `bun run lint` / `bun run lint:fix` / `bun run format`. Strict `recommended` ruleset, zero warnings allowed. `catch` blocks use `errMsg`/`errStatus` from `@/lib/utils` (no `e: any`); shared API shapes live in `@/lib/types`. Hook-dependency exceptions use a single-line `// biome-ignore lint/correctness/useExhaustiveDependencies: reason` directly above the dependency array.

The admin SPA at `internal/admin/web/` (**React 18 + React Router 7 + Vite + Tailwind 4 + shadcn/Radix + R3F**, managed with **bun, not npm**) is built into `internal/admin/web/dist/` and embedded via `//go:embed` in `internal/admin/admin.go`. The `//go:generate` directive there runs `bun install --frozen-lockfile && bun run build`. CI calls `make web` before `go build`, so the SPA is mandatory in releases. The same `dist` is re-served at `/` by the SaaS adapter when SaaS is enabled.

`make build` requires `bun` on PATH. Plain `go build ./...` works without bun if `internal/admin/web/dist/` already contains a build (or the embedded asset can be empty for backend-only iteration).

## The cc-core boundary (read this first)

`internal/` contains almost no credential/usage/pricing logic — those are cc-core packages imported and wired here. The server package imports: `cc-core/{auth, clienttoken, clientguard, pricing, ratelimit, requestlog, usage, thinkingsig, kiroapi, kirobridge}`. So when a path below says `auth.Pool` / `usage.Store` / `pricing.Catalog` / `clienttoken.Store` / `requestlog.Writer`, the **type lives in cc-core**, not this repo. There is no `internal/auth/` directory any more.

**Fingerprint code is duplicated on purpose.** cc-core has `mimicry/` + `sidecar/` (consumed by CPA-Claude), and hypitoken keeps a **vendored copy** in `internal/server/{fingerprint,mimicry,sidecar}.go` that its own proxy path actually uses (hypitoken does NOT import `cc-core/mimicry`/`cc-core/sidecar`). When you bump the CC fingerprint target you must edit **both** the cc-core copy and the hypitoken vendored copy, keep them byte-identical, then bump the `cc-core` dependency so CPA-Claude picks the change up too. The current target is **Claude Code 2.1.170** (ground truth in `crack/cc2170/`).

## Architecture (the parts that span files)

### Endpoint × provider matrix

`internal/server/server.go` constructs **N gin engines, one per enabled endpoint**. Each engine is bound to one provider (`auth.ProviderAnthropic` or `auth.ProviderOpenAI`) and serves only the routes that make sense for it. The "primary" endpoint (Claude if enabled, else Codex) additionally hosts the admin panel, public `/status`, the embedded SPA, and (when enabled) the SaaS `/api/v2/*` routes. The shop endpoint is an independent gin engine on its own listener.

The shared pieces (`auth.Pool`, `usage.Store`, `clienttoken.Store`, `pricing.Catalog`, `requestlog.Writer`, `ratelimit.RPM`/`Concurrency`, the `sidecarMgr`, the SaaS adapter, the Kiro state) live on `Server` and are injected into the engines. The split-by-engine matters because per-provider stickiness, concurrency budgets, and RPM limits all key on `(provider | clientToken)` — Claude saturation must NOT block a client's Codex traffic.

### Credential pool & sticky sessions (`cc-core/auth`)

`Pool.Acquire(ctx, provider, clientToken, group, model, exclude...)` picks an OAuth credential by:

1. Sticky reuse — if `clientToken` already has a healthy assignment for this provider, return it.
2. Fewest-active-sessions among healthy OAuth in the matching group with spare `max_concurrent`.
3. API-key fallback when every OAuth is saturated/quota-exceeded/dead.

A "session" is one `(provider, client_token, sessionID)` slot observed within `ActiveWindow` (default **5 min**, `active_window_minutes`), where `sessionID` is the client's per-window identifier — `clientSlotID` in `proxy.go` reads it from `X-Claude-Code-Session-Id` (Claude Code) or `Session_id` (Codex CLI), falling back to `""` (one slot per token) for raw API callers. One user opening N CLI windows presents N independent sessions and can be load-balanced across N credentials. Per-token RPM and concurrency caps deliberately stay keyed on the token alone, so opening more windows doesn't multiply a client's rate budget. `Pool.Acquire`/`Release`/`Unstick` all take the `sessionID` and must be passed the same value across the request. `Pool.Release` is called once per request to keep the active counter accurate. `Pool.Unstick` breaks the assignment when an upstream error suggests the credential is bad. `Pool.ReportUpstreamError` translates 401/403/429 into the right combination of cooldown / hard-failure / stealth-ban detection — shared by Anthropic and Codex paths, so changes ripple everywhere.

Health states live on `auth.Auth`: `MarkSuccess` / `MarkFailure` / `MarkHardFailure` / `MarkRateLimited` / `MarkUsageLimitReached` / `MarkClientCancel`. Hard failures are sticky (manual clear from admin) except `Pool.RunDailyAnthropicAPIKeyReset`, which wipes API-key hard-failures daily so a transient overnight outage doesn't pin them dead forever.

### Anthropic forward path (`internal/server/proxy.go`)

`forward()` is the per-request entry: SaaS pre-check (balance/caps) → legacy weekly-USD budget → RPM gate → concurrency gate → Kiro routing attempt (`tryKiro`, see below) → `forwardWithFailover`. `forwardWithFailover` is the retry loop — up to **`maxAttempts = 12` rounds** on *different* credentials (backstop, not a target; it normally stops as soon as a healthy credential succeeds or all are excluded). Per attempt, `doForward` (OAuth) or `doForwardAnthropicAPIKey` (API key) talks to upstream; the last credential-level error is withheld and replayed if every credential is exhausted.

The OAuth path applies **two layers of mimicry** to look like a real Claude Code **2.1.170** client:

- **Header layer** — `applyAnthropicHeaders` in `proxy.go` sets pinned `User-Agent`, `X-Stainless-*`, `Anthropic-Beta`, `X-App`, `X-Claude-Code-Session-Id`, `X-Client-Request-Id`. Constants live in `internal/server/fingerprint.go` (`CLICurrentVersion`, `claudeCLIUserAgent`, `claudeAnthropicBetaFull`, `claudeReportedBetas`, …). When you bump the CC version, **all of these move together** or the User-Agent disagrees with the `cc_version=` baked into the body billing block — itself a fingerprint signal. Note the request-header beta list (`claudeAnthropicBetaFull`) and the telemetry beta list (`claudeReportedBetas`) **diverged at 2.1.170** — don't derive one from the other.
- **Body layer** — `applyClaudeCodeBodyMimicry` in `mimicry.go` rewrites system into the canonical 4-block CC layout `[billing, "You are Claude Code...", ...originalSystem-with-cache_control]`, sets `metadata.user_id` to the JSON `{device_id, account_uuid, session_id}` shape, signs `cch=<xxhash5>` of the final body. The client's original prompt is preserved verbatim. **Skipped for Haiku models** and for requests whose system already starts with the CC prompt prefix (real CLI passing through).

Thinking-block signatures are sanitized via `cc-core/thinkingsig` (account-switch detection + a 400-signature-error recovery path in `doForward`). `maybeDecompressResponse` transparently un-gzips/un-brs upstream responses because we advertise `Accept-Encoding: gzip, br` to match real CC, but every internal path wants plain bytes.

### SimIdentity — the per-account fingerprint anchor

`SimIdentity{ AccountKey, AccountUUID, ClientToken }` (in `mimicry.go`) ties together every identity-bearing field:

- `DeviceIDFor(AccountKey)` — sha256-anchored, **identical for all requests routed through the same OAuth account** (across credential file rotations, across multiple client tokens). Mimics real CC's `machine-id sha256`.
- `SessionIDFor(id, body)` — derived from `(account, clientToken, sha256(first user message))` so a multi-turn conversation keeps one session_id but a new conversation rotates. Powers both the body's `metadata.user_id.session_id` and the `X-Claude-Code-Session-Id` header.
- `AccountKey()` on `auth.Auth` falls back through `AccountUUID > Email > ID`. Old credentials still work via the email fallback; new logins capture the real UUID from the token-exchange response.

**Invariant:** for one OAuth account routed by N downstream client tokens, upstream sees one device with N concurrent CC sessions — exactly what one user opening multiple `claude` windows looks like. Don't change this without re-checking every identity derivation. Machine-specific telemetry axes (`linux_distro_id`, `linux_kernel`, terminal, shell) are per-account via `cc-core auth.HostProfile`, so distinct accounts don't all advertise one identical host.

### Sidecar (auxiliary traffic emulation) — `internal/server/sidecar.go`

`sidecarMgr.Notify(a, clientToken)` is called from `doForward` after credential acquisition; first-touch of a `(account, clientToken)` pair fires bootstrap + heartbeat:

1. **bootstrap burst** — the GET/POST sidecars real CC fires at process start (GrowthBook, oauth/account/settings, grove, bootstrap `?model=claude-fable-5`, penguin, quota probe, mcp-registry, v1/mcp_servers, **`/v1/code/triggers`** behind beta `ccr-triggers-2026-01-30`, downloads/releases), each with its own captured `User-Agent` (Bun / axios / claude-code / claude-cli) and `Anthropic-Beta`. The first business `/v1/messages` waits up to `bootstrapWaitCap` (5s) for the quota probe to land.
2. **event_logging heartbeat** — POSTs `/api/event_logging/v2/batch` every ~18s ±40% with a `tengu_dir_search` ClaudeCodeInternalEvent (`model: claude-fable-5[1m]`, `betas: claudeReportedBetas`).
3. **Datadog heartbeat** — defined (`runDatadogHeartbeat`, `DD-API-KEY: pubea5604404508cdd34afb69e6f42a05bc`) but **deliberately disabled/unwired** (see the golangci exclusion). Constants are kept aligned for correctness.

A `bootstrapSessionID` is shared by all streams. GC evicts virtual sessions idle > 30 min; heartbeats self-stop after idle. `Server.Shutdown` cancels every live session's context. **API-key credentials never trigger sidecars** — the third-party-detection signal only applies to OAuth subscription accounts. `internal/server/sidecar_test.go` runs against a live `httptest.Server` with real timing (~23s); its `wants` map asserts each endpoint's `User-Agent` + `Anthropic-Beta`.

### Kiro path (`internal/kirocreds` + `internal/kiroupstream` + `internal/server/kiro.go`)

A second upstream: AWS CodeWhisperer / **Kiro**, exposed Anthropic-compatibly. `forward()` calls `tryKiro` *before* `forwardWithFailover` — if the client token's groups contain a `token_group` with `upstream: kiro` (`config.UpstreamKiro`) that accepts the model, the request is converted via `cc-core/kirobridge` (`Origin: KIRO_CLI` fingerprint), forwarded with `cc-core/kiroapi`, and the SSE stream translated back to Anthropic shape. Billing applies the group's `discount` (default `0.05`, i.e. 5% of official). Credentials are PKCE-OAuth, managed by `internal/kirocreds` (store = one JSON file per account under `kiro_auths/`, id = `sha256(access|refresh)[:12]`), selected round-robin by `internal/kiroupstream` with a 60s quota cache. Admin CRUD + PKCE login at `/admin/api/kiro/*` (`internal/admin/kiro.go`).

### SaaS multi-tenant layer (`internal/saas`, optional)

Enabled via `saas.enabled`. Mounts `/api/v2/*` on every engine and re-serves the SPA at `/`. Plugs into the proxy through the `SaaSAdapter` interface (`internal/server/saas_adapter.go`): `Lookup(token)` → user/wallet, `PreCheck` (balance + daily/monthly USD caps), `Charge` (official cost × pricing-group multiplier, default Claude 0.3 / Codex 0.05), `CredentialGroup`. Email+password auth (OTP verify) + JWT; USD wallet in a `wallet_tx` ledger; top-ups via Z-Pay / Alipay direct / Stripe Payment Element. Single SQLite DB (`internal/saas/db`, migrations v1–v4). SaaS admins can SSO into the legacy `/admin/api/*` with their JWT (GET = any authed user, writes = `role=admin`).

### Codex path (`codex_proxy.go` + `codex_oauth_proxy.go`)

OpenAI-format requests on the Codex endpoint. **API-key credentials** forward to `api.openai.com` mostly verbatim; **OAuth (ChatGPT Plus/Pro/Team)** credentials forward to `chatgpt.com/backend-api/codex/responses` with the Codex CLI session/account headers. JWT parsing in `cc-core/auth/codex_jwt.go` extracts `chatgpt_account_id` / `chatgpt_plan_type`; `cc-core/auth/codex_models.go` synthesizes a per-plan `/v1/models` catalog.

> **Codex OAuth has not been smoke-tested against a real ChatGPT subscription token in production.** Auth-layer paths (token exchange, refresh, JWT) work; full request/response parity against `chatgpt.com/backend-api` is pending. If you change this path, exercise both the API-key and OAuth branches.

### Shop (发卡网) + legal pages

`internal/shop` — an independent card-vending storefront on `:8319` with its own SQLite (`shop.db`), Stripe Hosted Checkout (card + Alipay, CNY/USD), products/orders/card-secrets, server-rendered Go `html/template` (not the SPA). `internal/legal` — static payment-DDQ pages (`/legal/{pricing,subscribe,support,refund-policy}`) for payment-provider review. Both are zero-dependency on the SaaS DB and independently toggleable.

### Capture archive — `crack/`

Ground truth for every fingerprint constant. The current target is **Claude Code 2.1.170** in **`crack/cc2170/`** (`SPEC.md` = the authoritative diff + edit checklist; `rows/` = structurally-redacted representative requests). Older benign-session captures (`crack/oauth/`, `crack/apikey/`, `crack/login/`) keep bodies verbatim and use the `split.py → sanitize.py → gen.py` pipeline; the live-session captures (`crack/cc2170/`) use **`crack/scripts/extract_live.py`**, which keeps only fingerprint-bearing *structure* and `<masked>`/`<redacted>`s all identity + prose. The raw whistle dump is never committed.

**When bumping the CC version target:** capture a fresh whistle dump → `extract_live.py <dump> crack/cc<ver>/rows` → write `crack/cc<ver>/SPEC.md` as the diff → update the fingerprint constants in **both** cc-core (`mimicry/` + `sidecar/`) and hypitoken's vendored copy, keeping them byte-identical → bump the `cc-core` dependency in hypitoken and CPA-Claude. See `crack/cc2170/SPEC.md` for the worked example (the 2.1.167 → 2.1.170 bump).

## Conventions worth knowing

- **bun, not npm** — every JS toolchain invocation uses bun; the lockfile is `bun.lock`.
- **All identity derivation is content-addressed**, no random UUIDs except `X-Client-Request-Id` and the internal `event_id`. Derive new stable identifiers from `accountKey` (or `accountKey + clientToken` if they should differ per downstream user).
- **OAuth credential file fields are append-only** — `parseFile` in `cc-core/auth/oauth.go` tolerates missing fields; new fields use the `_ = raw["new_field"].(...)` pattern so old credential files keep loading.
- **Per-provider stickiness uses `auth.NormalizeProvider(provider) + "|" + clientToken`** as the key — Claude and Codex share a token but not a slot. Don't collapse this.
- **Hop-by-hop + ingress headers are stripped before forwarding** (`hopHeaders` map and `stripIngressHeaders` in `proxy.go`). Critical behind Cloudflare Tunnel — `Cdn-Loop: cloudflare` triggers CF's loop-prevention WAF on `api.anthropic.com`. Don't loosen this filter.
- **Three SQLite DBs are independent**: SaaS (`saas.db`), shop (`shop.db`), and cc-core's usage state — each toggles separately.
