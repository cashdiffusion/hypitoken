# crack/cc2170 — Claude Code 2.1.170 live capture

Captured 2026-06-10 from a real `claude-cli/2.1.170` OAuth session (whistle dump,
100 sessions). **Supersedes crack/cc2156** (2.1.156) as the ground truth for the
fingerprint constants. The running code is pinned at 2.1.167; see `SPEC.md` for
the 2.1.156 → 2.1.170 diff and the edit checklist.

- **`SPEC.md`** — the authoritative 2.1.156 → 2.1.170 diff + edit checklist. Read
  this first; it is what `cc-core/mimicry`, `cc-core/sidecar`, and hypitoken's
  vendored copies should be pinned against.
- **`rows/`** — structurally-redacted representative requests, one per endpoint
  class. Produced by `crack/scripts/extract_live.py`. This capture adds three
  classes over cc2156: `bootstrap`, `code_triggers` (NEW endpoint), and
  `plugins_latest` (NEW endpoint). `count_tokens` was absent this session.

## Headline changes vs 2.1.156 / the 2.1.167 pin

- **Request-header `anthropic-beta`** (`/v1/messages`): dropped `context-1m-2025-08-07`,
  added `server-side-fallback-2026-06-01` + `fallback-credit-2026-06-01` (14 → 15 items).
- **Telemetry `betas` DIVERGED from the request header** — it still reports the
  old 9-item list (with `context-1m`). Don't regenerate one from the other.
- Default model is now **`claude-fable-5`** (bootstrap param, telemetry `model`)
  — was `claude-opus-4-8`.
- Two **new startup endpoints**: `GET /v1/code/triggers` (beta
  `ccr-triggers-2026-01-30`) and `GET …/plugins/claude-plugins-official/latest`.
- Datadog body grew: `process_metrics`, `swe_bench_*`, `subscription_type`, `rh`,
  `user_bucket`, `feature_name`.

## Privacy note

This is a *real working session*, so `extract_live.py` keeps only the
fingerprint-bearing **structure** — keys, block types, `cache_control`, versions,
betas, env, metadata shape — and replaces conversation prose, code, tool
descriptions, and identity values (device_id / account_uuid / organization_uuid /
session_id / email / event_id) with `<redacted …>` / `<masked:…>` placeholders.
The raw dump is never committed. The only verbatim secret-looking value retained
is the Datadog `dd-api-key` (`pub…`), which is a public global telemetry key, not
account-bound.

To re-extract from a fresh dump (whistle `/cgi-bin/get-data` response saved to a
file, or any dump with the `data.data` session map):

```bash
python3 crack/scripts/extract_live.py /path/to/whistle-dump.json crack/cc2170/rows
```
