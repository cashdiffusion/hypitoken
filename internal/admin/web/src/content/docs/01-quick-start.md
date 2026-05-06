---
slug: quick-start
title: Quick start
group: Getting started
order: 1
intro: One unified gateway fronts both Claude and Codex. Account → token → first request in under five minutes.
---

## One address, two providers

| Tool | Base URL |
| --- | --- |
| Claude Code | `https://api.novadiffusion.com` |
| Codex CLI | `https://api.novadiffusion.com/v1` |

The same API token works for both. Caddy routes `/v1/chat/*`, `/v1/responses*`, and `/v1/models` to the Codex endpoint; everything else goes to Claude.

> Only the official Claude Code and Codex CLI clients are supported.
> Third-party SDKs and tools (Anthropic SDK, OpenAI SDK, LiteLLM, custom
> chat clients, etc.) are blocked at the gateway by client-fingerprint
> checks — requests without a valid Claude Code or Codex CLI signature
> are rejected with `403 client not allowed`.

## Create an account

Register with email + password at `/register`.

## Issue a token

Go to **Tokens → New token**. Per-token limits you can set:

- Daily spend cap (USD)
- Monthly spend cap (USD)
- Concurrency limit
- RPM cap

## First call

```bash
curl https://api.novadiffusion.com/v1/messages \
  -H "Authorization: Bearer sk-cpa-•••" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 200,
    "messages": [{"role":"user","content":"Hello"}]
  }'
```

> **That's it.** The request is routed through the credential pool and the response is returned unchanged.
