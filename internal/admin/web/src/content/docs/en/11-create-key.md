---
slug: create-key
title: Create a key
group: Account & keys
order: 11
intro: Create one or more API keys from the Tokens page for different projects or clients.
---

## Steps

### 1. Open the Tokens page

Log in to the console and click **Tokens** in the left menu, or go straight to [api.novadiffusion.com/app/tokens](https://api.novadiffusion.com/app/tokens).

### 2. Create a key

Click "Create", give it a name (e.g. "Claude Code work key" to tell uses apart). Optionally set a **monthly spending cap** (USD, resets each calendar month). Save.

### 3. Copy the key

Once created, the key appears in the list as a long string starting with `sk-cpa-`. Click the **Copy** button next to it — **don't select the text by hand**; the copy button guarantees no stray spaces or newlines.

> **Keep your key safe.** Never commit it to Git or share it. If it leaks, delete it in the console immediately and create a new one.

## Why multiple keys

A dedicated key per project / client lets you:

- separate usage stats;
- revoke only the affected one when something goes wrong;
- assign per-person / per-project access in a team.

> Concurrency and RPM use server defaults. Contact an admin for custom limits. Next: [Top up](/docs/top-up).
