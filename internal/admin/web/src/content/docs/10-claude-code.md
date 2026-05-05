---
slug: claude-code
title: Claude Code setup
group: Clients
order: 10
intro: Drop the official Claude Code CLI onto HypiToken in under 30 seconds.
---

## Install Claude Code

```bash
# macOS / Linux (Homebrew)
brew install anthropic/tap/claude

# or via npm
npm install -g @anthropic-ai/claude-code

# verify
claude --version
```

## Point it at HypiToken

Set two environment variables — one for the base URL, one for your bearer
token. Add them to `~/.zshrc` / `~/.bashrc` to make them permanent.

```bash
export ANTHROPIC_BASE_URL="https://your.host"
export ANTHROPIC_API_KEY="sk-cpa-••••••••••••••••••••••••••••••••••••••••••••••••"
```

> **Why `ANTHROPIC_API_KEY`?** Claude Code uses the same env var for both
> Anthropic OAuth and API key auth. Our token slots in as a Bearer header,
> identical wire format.

## Run it

```bash
# interactive session against your wallet
claude

# one-shot prompt; bills against your wallet on completion
claude "summarise the diff"
```

## Available models

- `claude-haiku-4-5` — fastest, lowest cost
- `claude-sonnet-4-6` — balanced
- `claude-opus-4-7` — most capable, highest cost

> **Subscription pricing.** Your wallet is billed in real USD; the per-tier
> RMB peg is applied automatically. See [Pricing & billing](/docs/pricing-billing)
> for the exact formula.
