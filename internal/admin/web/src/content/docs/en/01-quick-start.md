---
slug: quick-start
title: Quick start
group: Getting started
order: 1
intro: One unified gateway for Claude and Codex. Create an account, issue a token, and make your first request in under five minutes.
---

## One address, two providers

| Tool | Base URL |
| --- | --- |
| Claude Code | `https://api.novadiffusion.com` |
| Codex CLI | `https://api.novadiffusion.com/v1` |

The same API token works for Claude Code and Codex CLI. Caddy routes `/v1/chat/*`, `/v1/responses*`, and `/v1/models` to the Codex endpoint; everything else goes to Claude.

> Currently, only the official Claude Code and Codex CLI clients are supported.
> Third-party SDKs and tools (Anthropic SDK, OpenAI SDK, LiteLLM, custom
> chat clients, etc.) are blocked at the gateway by client-fingerprint
> checks. Requests without a valid Claude Code or Codex CLI signature
> are rejected with `403 client not allowed`.

## Create an account

Register with email + password at `/register`.

## Prepare your client

| System | Claude Code | Codex CLI |
| --- | --- | --- |
| macOS | Install and run directly from Terminal | Install and run directly from Terminal |
| Windows | Runs natively from PowerShell / CMD; install Git for Windows first | Official CLI docs currently recommend Windows 11 + WSL2 |

Windows users do not need WSL just to run Claude Code. WSL2 is only the official Windows path for Codex CLI at the moment; if you only use Claude Code, native PowerShell is enough.

## Recommended configuration

| Client | Recommended persistent setup | Temporary debugging |
| --- | --- | --- |
| Claude Code | `~/.claude/settings.json`, or `%USERPROFILE%\.claude\settings.json` on Windows | `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` environment variables |
| Codex CLI | `~/.codex/config.toml` + `~/.codex/auth.json`; use the matching user home on Windows/WSL | `OPENAI_BASE_URL` / `OPENAI_API_KEY` environment variables |

The client guides below prefer file-based configuration. Environment variables are useful for one-off debugging, but they are not the recommended long-term setup.

## Issue a token

Go to **Tokens → New token**. You can set:

- Monthly spending cap (USD, resets each calendar month)

Concurrency and RPM use server defaults. Contact an admin if you need custom limits.

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

> The request is routed through the credential pool and the upstream response is returned unchanged.
