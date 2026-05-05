---
slug: faq
title: FAQ
group: Reference
order: 60
---

## Can I refund top-ups?

Operator-side: yes, via **Admin → Users → Adjust**. Self-serve refunds are
not exposed to end users.

## What happens at rate limit?

The proxy retries automatically across upstream credentials. If your
token's RPM cap fires, the response is `429` with a `Retry-After` header.

## Where is my data stored?

Conversation contents pass through the proxy in memory only — they are not
logged. Wallet ledger, tokens, orders are persisted in SQLite.

## Can I bring my own API key?

Yes — operators add API keys via the admin **Credentials** tab. Add a key
under any pricing group; users in that group will route through it.
