# crack/cc2156 — Claude Code 2.1.156 live capture

Captured 2026-05-29 from a real `claude-cli/2.1.156` OAuth session (whistle dump,
100 sessions). Supersedes the 2.1.146 ground truth for the fingerprint constants.

- **`SPEC.md`** — the authoritative 2.1.146 → 2.1.156 diff + edit checklist. Read
  this first; it is what `cc-core/mimicry`, `cc-core/sidecar`, and hypitoken's
  vendored copies are pinned against.
- **`rows/`** — structurally-redacted representative requests, one per endpoint
  class (v1/messages, count_tokens, event_logging startup + steady, datadog,
  releases). Produced by `crack/scripts/extract_live.py`.

## Privacy note — why these rows differ from oauth/apikey/login

Those modes were recorded benign test sessions and keep bodies verbatim (with
`sanitize.py`'s literal/regex redaction). This capture is a *real working
session*, so `extract_live.py` keeps only the fingerprint-bearing **structure**
— keys, block types, `cache_control`, versions, betas, env, metadata shape — and
replaces conversation prose, code, tool descriptions, and identity values
(device_id / account_uuid / session_id / email / event_id) with
`<redacted …>` / `<masked:…>` placeholders. The raw dump is never committed.

To re-extract from a fresh dump:

```bash
python3 crack/scripts/extract_live.py /path/to/whistle-dump.json
```
