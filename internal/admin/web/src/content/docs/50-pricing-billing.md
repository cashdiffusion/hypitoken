---
slug: pricing-billing
title: Pricing & billing
group: Reference
order: 50
intro: How wallet charges are computed.
---

## The formula

```text
bill_usd = official_cost_usd
         × (peg_rmb_per_usd / live_cny_per_usd)
         × group_multiplier
```

| field | meaning |
| --- | --- |
| `official_cost_usd` | Per-1M-token rates from the pricing catalog. Same as Anthropic / OpenAI's published prices. |
| `peg_rmb_per_usd` | Per-tier virtual peg. Default Codex `0.5`, Claude `2.0`. |
| `live_cny_per_usd` | Public exchange rate, refreshed hourly (fallback `¥7.20`). |
| `group_multiplier` | Per-tier surcharge. Default `1.0`. |

## Worked example

Claude Sonnet request that officially costs `$0.10` at the default tier:

```text
bill = 0.10 × (2.0 / 7.2) × 1.0
     = 0.10 × 0.2778
     = $0.0278
```

The same request on Codex at peg `0.5`:

```text
bill = 0.10 × (0.5 / 7.2) × 1.0
     = $0.0069
```
