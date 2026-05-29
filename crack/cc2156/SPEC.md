# Claude Code 2.1.156 — fingerprint ground truth

Captured 2026-05-29 from a live `claude-cli/2.1.156` OAuth session (whistle dump,
100 sessions). This is the authoritative reference for the fingerprint constants in
`cc-core/mimicry`, `cc-core/sidecar`, and the vendored copies in
`hypitoken/internal/server/{fingerprint,mimicry,sidecar}.go`.

Client env (from the `event_logging` / datadog telemetry bodies):

```
version / version_base = 2.1.156
build_time             = 2026-05-28T18:30:33Z
node_version           = v24.3.0      arch = x64        platform = linux
sdk (@anthropic-ai)    = 0.94.0
```

The previous target was **2.1.146**; everything below is the 2.1.146 → 2.1.156 diff
plus a couple of pre-existing bugs the new capture exposed.

---

## 1. `/v1/messages?beta=true` — request headers (OAuth chat path)

Exact header values observed (18 requests, all identical bar volatile ids):

| header | value | vs 2.1.146 |
|---|---|---|
| `user-agent` | `claude-cli/2.1.156 (external, cli)` | **bumped** |
| `x-stainless-arch` | `x64` | same |
| `x-stainless-lang` | `js` | same |
| `x-stainless-os` | `Linux` | same |
| `x-stainless-package-version` | `0.94.0` | same |
| `x-stainless-retry-count` | `0` | same |
| `x-stainless-runtime` | `node` | same |
| `x-stainless-runtime-version` | `v24.3.0` | same |
| `x-stainless-timeout` | `600` | same |
| `anthropic-version` | `2023-06-01` | same |
| `anthropic-dangerous-direct-browser-access` | `true` | same |
| `x-app` | `cli` | same |
| `anthropic-beta` | **14-item list, see below** | **+2 items** |

### `anthropic-beta` request header (FULL — 14 items, exact order)

```
claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,effort-2025-11-24,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07
```

Diff from 2.1.146: inserted **`thinking-token-count-2026-05-13`** (after
`redact-thinking-2026-02-12`) and **`mid-conversation-system-2026-04-07`** (after
`prompt-caching-scope-2026-01-05`). All other items + order unchanged.

→ `mimicry.ClaudeAnthropicBetaFull` (cc-core) / `claudeAnthropicBetaFull` (hypitoken).

---

## 2. `/v1/messages` — request **body** shape

Top-level keys real CC 2.1.156 sends:

```
model, messages[], system[4], tools[12], metadata{user_id}, max_tokens,
thinking{type:"adaptive"}, context_management{edits:[…]},
output_config{effort:"high"}, diagnostics{previous_message_id}, stream
```

We deliberately **do not** inject `thinking` / `output_config` /
`context_management` / `diagnostics` — they are beta-gated and alter response
semantics for non-CC downstream clients. (Unchanged policy; comment updated to
2.1.156 and the new field names.)

### system block layout (4 blocks)

```
[0] text  cc=none            "x-anthropic-billing-header: cc_version=2.1.156.<3hex>; cc_entrypoint=cli; cch=<5hex>;"
[1] text  cc=none            "You are Claude Code, Anthropic's official CLI for Claude."
[2] text  cc=ephemeral 1h scope:global    <- second-to-last
[3] text  cc=ephemeral 1h    (no scope)    <- last
```

**BUG FIXED:** prior code put `scope:global` on the **last** block and plain
ephemeral on the **second-to-last** — the capture shows the reverse, consistently
across all 18 requests (`sysCC = ['-','-','S1h','e1h']`). Fix:
`applySystemCacheBreakpoints` → second-to-last gets `withGlobalScope=true`, last
gets `withGlobalScope=false`.

### last-message cache breakpoint

Last content block of the last message carries `cache_control: ephemeral 1h`
(no scope). **Unchanged** — current code already matches.

### `metadata.user_id` (JSON string, unchanged shape)

```json
{"device_id":"<sha256 hex>","account_uuid":"<uuid>","session_id":"<uuid>"}
```

---

## 3. `/v1/messages/count_tokens?beta=true`

Forwarded by the proxy, not synthesized — informational only.
- `user-agent`: `claude-cli/2.1.156 (external, cli)`
- `anthropic-beta`: `claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,token-counting-2024-11-01`
- body keys: `{model, messages, tools}` — no system, no metadata.

---

## 4. Telemetry: `POST /api/event_logging/v2/batch`

Headers:
- `user-agent`: `claude-code/2.1.156`
- `anthropic-beta`: `oauth-2025-04-20`
- `x-service-name`: `claude-code`
- `connection`: `close`

Per-event `event_data`:
- `model`: **`claude-opus-4-8[1m]`** (was `claude-opus-4-7[1m]`)
- `betas`: **9-item SHORT list (≠ the request header)** — see below
- `env`: see §6
- `process`, `additional_metadata`: base64-encoded JSON blobs (unchanged shape)
- `auth`: `{organization_uuid, account_uuid}`

### `betas` field reported in telemetry (SHORT — 9 items)

```
claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07
```

This is the first 9 of the request-header list (stops at
`mid-conversation-system`; drops `advisor-tool`, `advanced-tool-use`, `effort`,
`extended-cache-ttl`, `cache-diagnosis`). **Prior code reused
`ClaudeAnthropicBetaFull` here — wrong.** Add `mimicry.ClaudeReportedBetas`
(hypitoken: `claudeReportedBetas`) and use it for both telemetry streams.

Steady-state batches are a varied mix (`tengu_feature_ok`, `tengu_tool_use_progress`,
`tengu_sysprompt_boundary_found`, `tengu_api_cache_breakpoints`, `tengu_api_success`,
`tengu_dir_search`, `tengu_attachment_compute_duration`, …). Our heartbeat emits a
single `tengu_dir_search` per tick — a known, low-risk simplification (event *name*
variety is not a billing signal); documented, not changed, to avoid per-event
`additional_metadata` shape errors.

---

## 5. Telemetry: `POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs`

Headers:
- `user-agent`: **`axios/1.15.2`** (was `axios/1.13.6`)
- `dd-api-key`: `pubea5604404508cdd34afb69e6f42a05bc` — **unchanged / re-confirmed**
  global across this capture too. (Datadog heartbeat remains disabled in our
  sidecar; constants aligned for correctness.)
- `connection`: `close`

Body is a JSON array of flat events. vs 2.1.146 the body gained two keys:
**`renderer_mode`** and **`feature_name`**. `model` → `claude-opus-4-8`,
`betas` → the 9-item short list, `build_time` → new, plus `linux_distro_id` /
`linux_kernel` (see §6).

---

## 6. `env` block (event_logging) — exact 2.1.156 contents

```
platform=linux  node_version=v24.3.0  terminal=konsole  shell=zsh
package_managers=npm,yarn,pnpm  runtimes=bun,deno,node  is_running_with_bun=true
is_ci=false  is_claubbit=false  is_github_action=false  is_claude_code_action=false
is_claude_ai_auth=true  is_claude_code_remote=false  is_conductor=false
is_local_agent_mode=false  arch=x64  platform_raw=linux  vcs=git
deployment_environment=unknown-linux
version=2.1.156  version_base=2.1.156  build_time=2026-05-28T18:30:33Z
linux_distro_id=arch          <- NEW (was absent)
linux_kernel=7.0.10-arch1-1   <- NEW (was absent)
```

Diff: `build_time` bumped; **added `linux_distro_id` + `linux_kernel`** (we were
missing both). The block is a single pinned plausible-host profile (already pins
`konsole`/`zsh`/`x64`), so distro/kernel are pinned to match.

---

## 7. Sidecar bootstrap

- `claude_cli/bootstrap` URL model param: `claude-opus-4-7` → **`claude-opus-4-8`**.
- `downloads.claude.ai/claude-code-releases/latest` → returns `2.1.156` (plain
  text), confirming current latest. Step UA `axios/1.15.2`.
- Quota probe (`claude-haiku-4-5-20251001`) + its beta list: **not present in this
  mid-session capture** — left unchanged (no new evidence).

---

## Edit checklist (apply to cc-core AND hypitoken's vendored copies)

| # | where | change |
|---|---|---|
| 1 | fingerprint: `CLICurrentVersion` | `2.1.146` → `2.1.156` |
| 2 | fingerprint: UA const | `claude-cli/2.1.156 (external, cli)` |
| 3 | fingerprint: `…BetaFull` | 14-item list (§1) |
| 4 | fingerprint: **new** `…ReportedBetas` | 9-item list (§4) |
| 5 | body/mimicry: `applySystemCacheBreakpoints` | swap scope: 2nd-to-last=global, last=plain (§2) |
| 6 | sidecar: `uaAxios` | `axios/1.13.6` → `axios/1.15.2` |
| 7 | sidecar: bootstrap model param | `claude-opus-4-7` → `claude-opus-4-8` |
| 8 | sidecar: heartbeat `model` | `claude-opus-4-7[1m]` → `claude-opus-4-8[1m]` |
| 9 | sidecar: heartbeat+datadog `betas` | → `…ReportedBetas` |
| 10 | sidecar: env `build_time` | `2026-05-28T18:30:33Z` |
| 11 | sidecar: env + datadog | add `linux_distro_id=arch`, `linux_kernel=7.0.10-arch1-1` |
| 12 | sidecar: datadog body | add `renderer_mode`, `feature_name`; `model` → `claude-opus-4-8` |
| 13 | comments | `2.1.126`/`2.1.146` → `2.1.156` where they name the target |
