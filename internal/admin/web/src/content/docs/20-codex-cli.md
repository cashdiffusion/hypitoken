---
slug: codex-cli
title: Codex CLI setup
group: Clients
order: 20
intro: OpenAI's Codex CLI runs against the unified gateway via `/v1`.
---

## Install Codex CLI

```bash
# macOS / Linux
npm install -g @openai/codex

# verify
codex --version
```

## Point it at HypiToken

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="sk-cpa-••••••••••••••••••••••••••••••••"
```

> **Same key, same host.** Claude Code and Codex share the gateway domain
> and your API token. Caddy routes `/v1/chat/*`, `/v1/responses*`, and
> `/v1/models` to the OpenAI-compatible endpoint; everything else
> (`/v1/messages`) goes to Claude.

## Run it

```bash
# interactive Codex session
codex

# one-shot
codex --model gpt-5.3-codex "explain this stack trace"
```

## Direct API

The endpoint speaks both `/v1/chat/completions` and OpenAI's newer
`/v1/responses`. POST the OpenAI request shape, get the OpenAI response
shape — your existing tooling works unchanged.
