---
slug: claude-code
title: Claude Code setup
group: Clients
order: 10
intro: Point the official Claude Code CLI at the gateway in under 30 seconds. Advisor, extended thinking, and sub-agents keep their native behavior.
---

## Install Claude Code

<div data-tabs="os-install-cc">
<div data-tab="macOS">

```bash
# official native installer
curl -fsSL https://claude.ai/install.sh | bash

# or Homebrew
brew install --cask claude-code

# verify
claude --version
```

</div>
<div data-tab="Windows">

Claude Code supports native Windows. Install [Git for Windows](https://git-scm.com/downloads/win) first, then install from PowerShell:

```powershell
# official native installer
irm https://claude.ai/install.ps1 | iex

# or WinGet
winget install Anthropic.ClaudeCode

# verify
claude --version
```

WSL2 is no longer required just to run Claude Code. Use WSL2 only if your own development tools require a Linux environment.

</div>
<div data-tab="Linux">

```bash
# official native installer
curl -fsSL https://claude.ai/install.sh | bash

# verify
claude --version
```

</div>
</div>

## Point it at the gateway

The recommended setup is to put the gateway URL and token in Claude Code's official `settings.json`. Variables under `env` are applied to every session.

<div data-tabs="os-config-cc">
<div data-tab="macOS">

```bash
mkdir -p ~/.claude
nano ~/.claude/settings.json
```

Add:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "YOUR_TOKEN_HERE"
  }
}
```

</div>
<div data-tab="Windows">

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.claude"
notepad "$env:USERPROFILE\.claude\settings.json"
```

Add:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "YOUR_TOKEN_HERE"
  }
}
```

Open a new PowerShell session before running `claude`.

</div>
<div data-tab="Linux">

```bash
mkdir -p ~/.claude
nano ~/.claude/settings.json
```

Add:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "YOUR_TOKEN_HERE"
  }
}
```

</div>
</div>

> **`ANTHROPIC_AUTH_TOKEN`** is used internally by the official Claude Code OAuth flow. Setting it makes Claude Code send `Authorization: Bearer <token>`, which is exactly what the gateway expects.

### Temporary debugging

For a one-off test, set environment variables in the current shell:

<div data-tabs="os-env-cc">
<div data-tab="macOS">

```bash
export ANTHROPIC_BASE_URL="https://api.novadiffusion.com"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKEN_HERE"
```

</div>
<div data-tab="Windows">

```powershell
$env:ANTHROPIC_BASE_URL = "https://api.novadiffusion.com"
$env:ANTHROPIC_AUTH_TOKEN = "YOUR_TOKEN_HERE"
```

</div>
<div data-tab="Linux">

```bash
export ANTHROPIC_BASE_URL="https://api.novadiffusion.com"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKEN_HERE"
```

</div>
</div>

## Run it

```bash
# interactive session
claude

# one-shot prompt
claude "summarise the diff"

# advisor mode (Claude Code ≥ 2.1)
claude --advisor "review my architecture"
```

## Supported features

Core Claude Code features work through the gateway without extra configuration:

| Feature | Status |
| --- | --- |
| Advisor mode | ✓ |
| Extended thinking | ✓ |
| Sub-agents | ✓ |
| MCP tool use | ✓ |
| Prompt caching | ✓ |
| Streaming responses | ✓ |

## Available models

- `claude-haiku-4-5-20251001` — fastest
- `claude-sonnet-4-6` — balanced, recommended
- `claude-opus-4-7` — most capable

## How routing works

The gateway maintains a **sticky session** per client token: your Claude Code session lands on the same upstream credential within a 10-minute activity window. This preserves cache hits across turns and keeps conversation continuity intact.

If that credential becomes unavailable, the pool manager switches to the next healthy credential and retries the request.
