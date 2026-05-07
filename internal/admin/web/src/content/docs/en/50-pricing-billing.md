---
slug: pricing-billing
title: Usage & billing
group: Reference
order: 50
intro: Per-request billing based on official token counts, with transparent and auditable line items.
---

## How charges work

Every successful request is charged based on the official Anthropic / OpenAI token counts reported in the response. Operators can set per-group pricing multipliers; the formula is:

```text
bill_usd = official_cost_usd × group_multiplier
```

The default multiplier is `1.0`, which matches the official rate.

## Per-token limits

Each issued token can have independent limits:

| Limit | What it controls |
| --- | --- |
| Monthly cap (USD) | Resets each calendar month; set in the token creation dialog |
| Daily cap (USD) | Server-side field configurable by an admin |
| Concurrency | Maximum simultaneous requests, set by server defaults unless customized |
| RPM | Requests per minute, set by server defaults unless customized |

When a limit is reached, the gateway returns `429` with a `Retry-After` header.

## Request log

Every request is logged with timestamp, model, token counts, charge, status, and duration. The **Logs** page shows the full formula for each charge: input × rate + output × rate + cache R/W = official USD × multiplier = final wallet deduction.

## Viewing billing on each system

| System | What to do |
| --- | --- |
| macOS | Open `/app/billing` in your browser for wallet history and `/app/logs` for per-request charges |
| Windows | Use the same browser pages; if you are debugging a CLI from PowerShell, reopen the terminal after setting environment variables |

Billing is enforced server-side, so macOS and Windows are charged the same way. The only platform differences are CLI installation and environment-variable setup.
