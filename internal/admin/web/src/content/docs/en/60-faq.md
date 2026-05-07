---
slug: faq
title: FAQ
group: Reference
order: 60
---

## Does advisor mode work?

Yes. The gateway maintains a full Claude Code session identity per upstream credential, so advisor, extended thinking, sub-agents, and MCP tool use are forwarded with official CLI behavior.

## Do Windows users have to use WSL?

Not for Claude Code. Claude Code supports native Windows and can run from PowerShell / CMD. Codex CLI's current official Windows path still recommends Windows 11 + WSL2, so use WSL2 for Codex CLI if native PowerShell gives you trouble.

| System | Recommendation |
| --- | --- |
| macOS | Install both Claude Code and Codex CLI directly from Terminal |
| Windows | Run Claude Code natively from PowerShell; prefer WSL2 for Codex CLI |

## What happens at rate limit?

The proxy retries automatically across credentials in the pool. If your own token hits an RPM, concurrency, or spend limit, the response is `429` with a `Retry-After` header.

## Where is my data stored?

Conversation contents pass through the proxy in memory only and are not written to request logs. Token metadata, usage stats, and the billing ledger are persisted in SQLite.

## Can I bring my own API key?

Yes. Admins can add API keys or OAuth credentials from the **Credentials** tab. The pool scheduler picks a healthy credential for each request.

## What is a sticky session?

Each client token gets the same upstream credential for all requests within a 10-minute activity window. This preserves prompt-cache hits across turns and maintains advisor session continuity.
