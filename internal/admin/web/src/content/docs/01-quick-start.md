---
slug: quick-start
title: Quick start
group: Getting started
order: 1
intro: One unified address fronts both Claude and Codex. Signup → first billed request in under five minutes.
---

## One address, two providers

| Tool | Base URL |
| --- | --- |
| Claude Code (Anthropic) | `https://api.novadiffusion.com` |
| Codex CLI / OpenAI SDK | `https://api.novadiffusion.com/v1` |

The same API token works for both — billing is per-request, USD wallet,
priced from the live CNY/USD rate.

## Create an account

Register with email + password. Email verification is optional in dev.

## Top up your wallet

Open **Billing → Top up**. Pay via Alipay; the wallet is credited at the live
USD/CNY rate when the order is confirmed.

## Mint an API token

Go to **Tokens → New token**. Per-token caps you can set:

- Daily USD cap
- Monthly USD cap
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

> **That's it.** The charge lands in your wallet ledger as a `charge`
> transaction. Refresh `/app` to see the balance update.
