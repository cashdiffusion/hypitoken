# Claude Code 2.1.170 — fingerprint ground truth

Captured 2026-06-10 from a live `claude-cli/2.1.170` OAuth session (whistle dump,
100 sessions, 77 CC requests). This is the authoritative reference for the
fingerprint constants in `cc-core/mimicry`, `cc-core/sidecar`, and the vendored
copies in `hypitoken/internal/server/{fingerprint,mimicry,sidecar}.go`.

Supersedes **crack/cc2156** (Claude Code 2.1.156). The running code is pinned at
**2.1.167** (whose dedicated capture dir was never committed — `fingerprint.go`'s
`crack/claude/SPEC.md` pointer is dangling); the betas below show 2.1.167 → 2.1.170
moved, so treat this as the live target.

Client env (from the `event_logging` / datadog telemetry bodies):

```
version / version_base = 2.1.170
build_time             = 2026-06-09T15:09:09Z   (was 2026-05-28T18:30:33Z @ 2.1.156)
node_version           = v24.3.0                (unchanged)
sdk (@anthropic-ai)    = 0.94.0                 (unchanged, x-stainless-package-version)
linux_distro_id        = arch
linux_kernel           = 7.0.11-arch1-1         (was 7.0.10-arch1-1 @ 2.1.156)
default model (telem)  = claude-fable-5[1m]      (was claude-opus-4-8[1m] @ 2.1.156)
```

Everything below is the **2.1.156 → 2.1.170 diff** (the running 2.1.167 pin sits
between; the only fingerprint-relevant move since 2.1.156 is the request-header
beta list — see §1).

---

## 1. `/v1/messages?beta=true` — request headers (OAuth chat path)

Header *set* is identical to 2.1.156. Only `user-agent` and `anthropic-beta`
changed. No new headers on this endpoint.

| header | value | vs 2.1.156 |
|---|---|---|
| `user-agent` | `claude-cli/2.1.170 (external, cli)` | **bumped** |
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
| `anthropic-beta` | **15-item list, see below** | **−1, +2 (net 14→15)** |

### `anthropic-beta` request header (FULL — 15 items, exact order)

```
claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,effort-2025-11-24,server-side-fallback-2026-06-01,fallback-credit-2026-06-01,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07
```

**Diff vs 2.1.156 (the 14-item list the code still pins via 2.1.167):**

- **REMOVED** `context-1m-2025-08-07` (was item 3). The 1M-context beta is no
  longer advertised on the request header.
- **ADDED** `server-side-fallback-2026-06-01` and `fallback-credit-2026-06-01`,
  inserted **after `effort-2025-11-24`, before `extended-cache-ttl-2025-04-11`**.

These two new betas pair with the new `/api/claude_cli/bootstrap` model fallback
machinery (`additional_model_options`, §7) — server-side model fallback + credit.

→ `mimicry.ClaudeAnthropicBetaFull` (cc-core) / `claudeAnthropicBetaFull` (hypitoken).

---

## 2. `/v1/messages` — request **body** shape  (UNCHANGED vs 2.1.156)

Top-level keys real CC 2.1.170 sends — identical to 2.1.156:

```
model, messages[], system[4], tools[], metadata{user_id}, max_tokens,
thinking{type:"adaptive"}, context_management{edits:[{type:"clear_thinking_20251015",keep:"all"}]},
output_config{effort:"high"}, diagnostics{previous_message_id}, stream
```

We deliberately **do not** inject `thinking` / `output_config` /
`context_management` / `diagnostics` — beta-gated, alter response semantics for
non-CC downstream clients. (Unchanged policy.)

### system block layout (4 blocks) — UNCHANGED

```
[0] text  cc=none            "x-anthropic-billing-header: cc_version=2.1.170.<3hex>; cc_entrypoint=cli; cch=<5hex>;"
[1] text  cc=none            "You are Claude Code, Anthropic's official CLI for Claude."
[2] text  cc=ephemeral 1h scope:global    <- second-to-last
[3] text  cc=ephemeral 1h    (no scope)    <- last
```

Confirmed `sysCC = ['-','-','S1h','e1h']` (scope:global on the **second-to-last**
block, plain ephemeral on the **last**). Same as 2.1.156 — no change needed.

### `metadata.user_id` (JSON string) — UNCHANGED shape

```json
{"device_id":"<sha256 hex>","account_uuid":"<uuid>","session_id":"<uuid>"}
```

---

## 3. `/v1/messages/count_tokens?beta=true`

**Not present** in this mid-session capture (no token-counting happened in the
window). Forwarded by the proxy, not synthesized — no new evidence; assume
unchanged from 2.1.156.

---

## 4. Telemetry: `POST /api/event_logging/v2/batch`

Headers (unchanged vs 2.1.156):
- `user-agent`: `claude-code/2.1.170`
- `anthropic-beta`: `oauth-2025-04-20`
- `x-service-name`: `claude-code`
- `connection`: `close`

Per-event `event_data` keys: `event_name, client_timestamp, model, session_id,
user_type, betas, env, entrypoint, is_interactive, client_type, process,
additional_metadata, auth, event_id, device_id`.
- `model`: **`claude-fable-5[1m]`** (was `claude-opus-4-8[1m]`)
- `betas`: **9-item SHORT list — UNCHANGED, still contains `context-1m`** (below)
- `env`: see §6
- `auth`: `{organization_uuid, account_uuid}`

### `betas` field reported in telemetry (SHORT — 9 items) — UNCHANGED

```
claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07
```

> ⚠ **KEY SUBTLETY:** as of 2.1.170 the telemetry beta list and the request-header
> beta list have **DIVERGED**. The telemetry list still reports
> `context-1m-2025-08-07` (item 3) and stops at `mid-conversation-system`; the
> request header (§1) **dropped** `context-1m` and **added** the two fallback
> betas. They used to be "telemetry = first 9 of header" — that invariant no
> longer holds. `claudeReportedBetas` stays as-is; only `claudeAnthropicBetaFull`
> moves.

→ `mimicry.ClaudeReportedBetas` (cc-core) / `claudeReportedBetas` (hypitoken) —
**no change**.

---

## 5. Telemetry: `POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs`

Headers (unchanged):
- `user-agent`: `axios/1.15.2`
- `dd-api-key`: `pubea5604404508cdd34afb69e6f42a05bc` — **unchanged / re-confirmed**
  global across this capture too. (Datadog heartbeat remains disabled in our
  sidecar; constant kept aligned for correctness.)
- `connection`: `close`

Body is a JSON array of flat events. `model` → `claude-fable-5`, `betas` → the
9-item short list, `version`/`version_base` → `2.1.170`, `build_time` → new.

**Datadog event body keys grew** vs 2.1.156. Full 2.1.170 key set:

```
ddsource, ddtags, message, service, hostname, env, model, session_id, user_type,
betas, entrypoint, is_interactive, client_type, process_metrics,
swe_bench_run_id, swe_bench_instance_id, swe_bench_task_id,   <- swe_bench triad
subscription_type, rh, platform, platform_raw, arch, node_version, terminal,
shell, package_managers, runtimes, is_running_with_bun, is_ci, is_claubbit,
is_claude_code_remote, is_local_agent_mode, is_conductor, is_github_action,
is_claude_code_action, is_claude_ai_auth, version, version_base, build_time,
deployment_environment, linux_kernel, linux_distro_id, vcs,
feature_name, user_bucket
```

New keys not in the 2.1.156 capture: `process_metrics` (replaces the older
`process` blob on this stream), `swe_bench_run_id` / `swe_bench_instance_id` /
`swe_bench_task_id` (all null outside SWE-bench harness runs), `subscription_type`
(= `max`), `rh`, `user_bucket`, `feature_name` (e.g. `skill_load_commands_dir`,
`tengu_feature_ok`). `ddtags` carries `subscription_type:max`, `model:claude-fable-5`.

---

## 6. `env` block (event_logging) — exact 2.1.170 contents

```
platform=linux  node_version=v24.3.0  terminal=konsole  shell=zsh
package_managers=npm,yarn,pnpm  runtimes=bun,deno,node  is_running_with_bun=true
is_ci=false  is_claubbit=false  is_github_action=false  is_claude_code_action=false
is_claude_ai_auth=true  is_claude_code_remote=false  is_conductor=false
is_local_agent_mode=false  arch=x64  platform_raw=linux  vcs=git
deployment_environment=unknown-linux
version=2.1.170  version_base=2.1.170  build_time=2026-06-09T15:09:09Z
linux_distro_id=arch
linux_kernel=7.0.11-arch1-1
```

Diff vs 2.1.156: `version`/`version_base` → 2.1.170, `build_time` bumped,
`linux_kernel` 7.0.10 → 7.0.11. Structure identical (still the single pinned
plausible-host profile — `konsole`/`zsh`/`x64`/`arch`).

---

## 7. Sidecar bootstrap & new probes

- `claude_cli/bootstrap` URL model param: `claude-opus-4-8` → **`claude-fable-5`**
  (`?entrypoint=cli&model=claude-fable-5`). UA `claude-code/2.1.170`, beta
  `oauth-2025-04-20`. **Response body gained structure** (06-bootstrap.json):
  ```json
  {"client_data":null,
   "additional_model_options":[{"model":"claude-fable-5[1m]","name":"Fable",
       "description":"Most capable for your hardest and longest-running tasks","disabled_reason":null}],
   "additional_model_costs":null,
   "oauth_account":{"organization_type":"claude_max",
       "organization_rate_limit_tier":"default_claude_max_20x","user_rate_limit_tier":null,"seat_tier":null,…},
   "cwk_cfg_key":"marigold"}
  ```
- `downloads.claude.ai/claude-code-releases/latest` → returns `2.1.170` (plain
  text), confirming current latest. Step UA `axios/1.15.2`.

### NEW endpoints in 2.1.170 (not present in any prior capture)

1. **`GET /v1/code/triggers`** — CCR (Claude-Code-Remote) triggers poll.
   - `anthropic-beta`: `ccr-triggers-2026-01-30`  ← new single-item beta
   - `anthropic-client-platform`: `claude_code_cli`  ← new header (this endpoint only)
   - `anthropic-version`: `2023-06-01`, `user-agent`: `claude-cli/2.1.170 (external, cli)`
   - Response: `{"data":[],"has_more":false}` (200). Empty when no remote triggers.
2. **`GET /claude-code-releases/plugins/claude-plugins-official/latest`** — plugin
   manifest version pointer.
   - UA `axios/1.15.2`. Response: a bare 40-char git SHA
     (`df5224ba07bcc260c4c6bcd7ce2c5a6cff533c4a`), like the `releases/latest` probe.

Neither is auth-billing-relevant, but both are part of the real CC startup
traffic footprint. Add to the sidecar bootstrap suite if/when we want fuller
parity (low priority — they carry no usage signal).

---

## Edit checklist (apply to cc-core AND hypitoken's vendored copies)

The running code is at **2.1.167**; only items 1–4 are fingerprint-relevant for
chat traffic. The rest keep the sidecar/telemetry emulation accurate.

| # | where | change |
|---|---|---|
| 1 | fingerprint: `CLICurrentVersion` | `2.1.167` → `2.1.170` |
| 2 | fingerprint: UA const | `claude-cli/2.1.170 (external, cli)` |
| 3 | fingerprint: `…BetaFull` (request header) | **15-item list (§1)** — drop `context-1m-2025-08-07`, add `server-side-fallback-2026-06-01` + `fallback-credit-2026-06-01` after `effort` |
| 4 | fingerprint: `…ReportedBetas` (telemetry) | **NO CHANGE** — still 9 items incl. `context-1m` (§4). The two lists have diverged; do not regenerate ReportedBetas from BetaFull. |
| 5 | body/mimicry: system cache scope | unchanged (2nd-to-last=global, last=plain) — already correct |
| 6 | sidecar: `uaAxios` | `axios/1.15.2` — unchanged |
| 7 | sidecar: bootstrap model param | `claude-opus-4-8` → `claude-fable-5` |
| 8 | sidecar: heartbeat `model` | `claude-opus-4-8[1m]` → `claude-fable-5[1m]` |
| 9 | sidecar: heartbeat+datadog `betas` | `…ReportedBetas` — unchanged |
| 10 | sidecar: env `build_time` | `2026-06-09T15:09:09Z` |
| 11 | sidecar: env `linux_kernel` | `7.0.10-arch1-1` → `7.0.11-arch1-1` |
| 12 | sidecar: datadog body | add `process_metrics`, `swe_bench_*` triad, `subscription_type`, `rh`, `user_bucket`, `feature_name`, `user_bucket`; `model` → `claude-fable-5` |
| 13 | sidecar (optional): new probes | `/v1/code/triggers` (beta `ccr-triggers-2026-01-30`, header `anthropic-client-platform: claude_code_cli`) + `plugins/claude-plugins-official/latest` |
| 14 | comments | `2.1.156`/`2.1.167` → `2.1.170`; fix dangling `crack/claude/SPEC.md` → `crack/cc2170/SPEC.md` |
