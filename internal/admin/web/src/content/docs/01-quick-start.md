---
slug: quick-start
title: Quick start
group: Getting started
order: 1
intro: From signup to first billed request in under five minutes.
---

## Create an account

Register with email + password. SMTP verification is optional in dev.

## Top up your wallet

Open **Billing → Top up**. Pay via Alipay; the wallet is credited at the live
USD/CNY rate when the order is confirmed.

## Mint an API token

Go to the **Tokens** page and click **New token**. Per-token caps you can set:

- Daily USD cap
- Monthly USD cap
- Concurrency limit
- RPM cap

## First call

```bash
curl https://your.host/v1/messages \
  -H "Authorization: Bearer sk-cpa-•••" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 200,
    "messages": [{"role":"user","content":"Hello"}]
  }'
```

> **That's it.** The bill lands in your wallet ledger as a `charge`
> transaction. Refresh `/app` to see your balance update.
