---
slug: codex-cli
title: Codex CLI setup
group: Clients
order: 20
intro: OpenAI's Codex CLI works against the /v1/chat/completions and /v1/responses endpoints.
---

## Install Codex CLI

```bash
# Codex CLI lives on the OpenAI tap
brew install openai/tap/codex

# or via npm
npm install -g @openai/codex

# verify
codex --version
```

## Point it at HypiToken

Codex listens on a separate port (`8318` by default) so per-provider
concurrency and budgets don't share buckets.

```bash
export OPENAI_BASE_URL="https://your.host:8318/v1"
export OPENAI_API_KEY="sk-cpa-••••••••••••••••••••••••••••••••••••••••••••••••"
```

## Run it

```bash
# interactive Codex session
codex

# one-shot
codex --model gpt-5.3-codex "explain this stack trace"
```

## Native /v1/responses

If you're integrating directly, the Codex endpoint also serves OpenAI's
newer `/v1/responses` path natively. POST the same OpenAI request shape.
