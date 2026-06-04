---
slug: pricing
title: Usage & billing
group: Reference
order: 31
intro: Per-token billing in real time. No monthly fee, no minimum. Pay for exactly what you use.
---

## The billing model

HypiToken bills **per token, in real time**. Every request settles immediately:

```text
cost = input tokens × input price + output tokens × output price
```

Prices vary by model (stronger models cost more). **Cache-hit** input tokens are usually billed at a lower rate, so multi-turn conversations get cheaper.

## See live prices

Per-model rates are on the console's [Pricing page](https://api.novadiffusion.com/pricing) — prices may track upstream changes, and the page always shows the rates currently in effect.

## Usage & invoices

- See balance, top-up history, and spend under **Billing** in the console.
- See each request's model, token counts, and charge under **Logs**.
- For per-project accounting, give each project its own [key](/docs/create-key).

## Credit & limits

- **Trial credit**: granted to new accounts to try things out.
- **Monthly cap**: set a per-key monthly spending cap (USD, resets each calendar month) to prevent surprises.
- **Concurrency / RPM**: server defaults; contact an admin for custom limits.

> When the balance runs out, the API returns `402 Payment Required`. Enable the low-balance alert.
