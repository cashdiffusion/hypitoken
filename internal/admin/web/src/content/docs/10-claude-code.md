---
slug: claude-code
title: Claude Code setup
group: Clients
order: 10
intro: Drop the official Claude Code CLI onto HypiToken in under 30 seconds.
---

## Install Claude Code

```bash
# macOS / Linux
npm install -g @anthropic-ai/claude-code

# verify
claude --version
```

> **Windows.** Install [WSL2](https://learn.microsoft.com/windows/wsl/install)
> first, then run the npm command inside Ubuntu.

## Point it at HypiToken

Set two environment variables. Add them to `~/.zshrc` / `~/.bashrc` to make
them permanent.

```bash
export ANTHROPIC_BASE_URL="https://api.novadiffusion.com"
export ANTHROPIC_AUTH_TOKEN="sk-cpa-••••••••••••••••••••••••••••••••"
```

> **`ANTHROPIC_AUTH_TOKEN`** is what real Claude Code's OAuth flow uses
> internally. Setting it makes CC send `Authorization: Bearer <token>` —
> which is exactly what HypiToken expects.

## Run it

```bash
# interactive session against your wallet
claude

# one-shot prompt
claude "summarise the diff"
```

## Available models

- `claude-haiku-4-5` — fastest, cheapest
- `claude-sonnet-4-6` — balanced
- `claude-opus-4-7` — most capable

> **Pricing.** Wallet is billed in real USD; the per-tier RMB peg is applied
> automatically. See [Pricing & billing](/docs/pricing-billing) for the
> formula.
