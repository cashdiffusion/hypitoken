---
slug: pricing-billing
title: Usage & billing
group: Reference
order: 50
intro: Per-request billing based on official token counts. Transparent and auditable.
---

## How charges work

Every successful request is charged based on the official Anthropic / OpenAI token counts reported in the response. The operator sets per-group pricing multipliers; the formula is:

```text
bill_usd = official_cost_usd × group_multiplier
```

Default multiplier is `1.0` — you pay exactly the official rate.

## Per-token limits

You can cap each issued token independently:

| Limit | What it controls |
| --- | --- |
| Daily cap (USD) | Resets at midnight UTC |
| Monthly cap (USD) | Resets on the 1st |
| Concurrency | Max simultaneous requests |
| RPM | Requests per minute |

When a cap is reached the gateway returns `429` with a `Retry-After` header.

## Request log

Every request is logged with timestamp, model, token counts, cost, status, and duration. Accessible in the admin panel under **Requests**.
