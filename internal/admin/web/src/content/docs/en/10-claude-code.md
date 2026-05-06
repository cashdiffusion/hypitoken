---
slug: claude-code
title: Claude Code setup
group: Clients
order: 10
intro: Drop the official Claude Code CLI onto the gateway in under 30 seconds. Advisor, extended thinking, and sub-agents all work natively.
---

## Install Claude Code

```bash
# macOS / Linux
npm install -g @anthropic-ai/claude-code

# verify
claude --version
```

> **Windows.** Install [WSL2](https://learn.microsoft.com/windows/wsl/install) first, then run the npm command inside Ubuntu.

## Point it at the gateway

Set two environment variables. Add them to `~/.zshrc` / `~/.bashrc` to make them permanent.

```bash
export ANTHROPIC_BASE_URL="https://api.novadiffusion.com"
export ANTHROPIC_AUTH_TOKEN="sk-cpa-••••••••••••••••••••••••••••••••"
```

> **`ANTHROPIC_AUTH_TOKEN`** is what the official Claude Code OAuth flow uses internally. Setting it makes CC send `Authorization: Bearer <token>` — which is exactly what the gateway expects.

## Run it

```bash
# interactive session
claude

# one-shot prompt
claude "summarise the diff"

# advisor mode (Claude Code ≥ 2.1)
claude --advisor "review my architecture"
```

## Supported features

All Claude Code features work through the gateway without any extra configuration:

| Feature | Status |
| --- | --- |
| Advisor mode | ✓ |
| Extended thinking | ✓ |
| Sub-agents | ✓ |
| MCP tool use | ✓ |
| Prompt caching | ✓ |
| Streaming responses | ✓ |

## Available models

- `claude-haiku-4-5-20251001` — fastest
- `claude-sonnet-4-6` — balanced, recommended
- `claude-opus-4-7` — most capable

## How routing works

The gateway maintains a **sticky session** per client token: your CC sessions always land on the same upstream credential within a 10-minute activity window. This preserves cache hits across turns and keeps conversation continuity intact.

If that credential becomes unavailable, the pool manager promotes the next healthiest one automatically — no dropped requests.
