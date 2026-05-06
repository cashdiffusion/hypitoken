---
slug: faq
title: FAQ
group: Reference
order: 60
---

## Does advisor mode work?

Yes. The gateway maintains a full Claude Code session identity per upstream credential, so advisor, extended thinking, sub-agents, and MCP tool use all work exactly as the official CLI intends.

## What happens at rate limit?

The proxy retries automatically across credentials in the pool. If your token's own RPM or concurrency cap fires, the response is `429` with a `Retry-After` header.

## Where is my data stored?

Conversation contents pass through the proxy in memory only — they are not logged. Token metadata, usage stats, and billing ledger are persisted in SQLite.

## Can I bring my own API key?

Yes — add API keys or OAuth credentials via the admin **Credentials** tab. The pool scheduler picks the healthiest credential for each request.

## What is a sticky session?

Each client token gets the same upstream credential for all requests within a 10-minute activity window. This keeps prompt-cache hits intact across conversation turns and maintains session continuity for advisor.
