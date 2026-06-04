---
slug: faq
title: FAQ
group: Reference
order: 30
intro: The most common questions about connecting and billing.
---

## 1. Requests return 401 Unauthorized after configuring?

Check: ① the key is copied in full (with the `sk-cpa-` prefix); ② the config took effect (reopen the terminal / restart the client); ③ the key isn't disabled or deleted.

## 2. Temporary env vars don't survive a new terminal?

Variables set with `export` (Linux/macOS) or `$env:` (PowerShell) only live in the current session. Use the **permanent** method (`settings.json` / `config.toml`) from each client guide.

## 3. Why are the Base URLs different for Claude Code and Codex?

Different protocols:

- Claude Code uses Anthropic — Base URL `https://api.novadiffusion.com` (**no** `/v1`)
- Codex / Cursor use OpenAI — Base URL `https://api.novadiffusion.com/v1` (**must** include `/v1`)

## 4. Why can't I test the endpoint with curl / a Python SDK / Postman?

The gateway only accepts **interactive AI coding clients** (Claude Code, Codex CLI, Cursor, Claude Desktop). Raw SDKs, `curl`, `python-requests`, LiteLLM, and Postman are rejected at ingress with `403 client not allowed`. Connect with one of the official clients — don't test with bare `curl`.

## 5. How is billing calculated?

Per token, in real time: after each request, `input tokens × input price + output tokens × output price` is deducted. Prices vary by model — see the [Pricing](/docs/pricing) page. When the balance hits zero, the API returns `402 Payment Required`.

## 6. Are streaming responses supported?

Yes. Fully compatible with Anthropic / OpenAI streaming SSE, no extra config — clients stream by default.

## 7. Can one key be used in multiple tools at once?

Yes. The same key works across Claude Code, Codex CLI, and Cursor — just use the right Base URL per protocol. For per-project usage stats, create a dedicated key per project. (CCSwitch isn't a gateway client; it only manages the provider config for Claude Code / Codex.)

## 8. How does the gateway differ from the official API?

- **Network**: reachable directly without a VPN, lower latency
- **Account**: a gateway-issued key, not an official one
- **Billing**: top up here, pay per usage, usually cheaper
- **Models**: Claude, GPT and more without separate vendor accounts
- **Limits**: model versions are per the console

## 9. Never used a command line — what now?

Read [First-timers](/docs/beginners) — it covers opening a terminal, copy/paste, and placeholders. Or pick [Cursor](/docs/cursor) (a GUI) and skip the command line entirely.

## 10. I copied an extra space / newline with the key — now what?

That's a common cause of 401s. Go back to [Tokens](https://api.novadiffusion.com/app/tokens) and use the **Copy** button next to the key (don't select the text by hand) — it guarantees no stray characters. Then re-paste into the client.
