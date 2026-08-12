---
slug: codex-cli
title: Codex CLI setup
group: Clients
order: 20
intro: OpenAI's official Codex CLI connects to the unified gateway through /v1. The same key works for Claude Code too.
---

## Before you start

- Registered and created a key ([quick start](/docs/quick-start))
- Have balance or remaining trial credit ([how to top up](/docs/top-up))
- Know how to open a terminal ([never used one?](/docs/beginners))

## 1. About Codex CLI

Codex CLI is OpenAI's official terminal coding assistant — drive code, run commands, and read/write files in natural language from the shell.

| | Codex CLI | Claude Code |
| --- | --- | --- |
| Maker | OpenAI | Anthropic |
| Protocol | OpenAI | Anthropic |
| Node version | **v22+** | v18+ |
| Env vars | `OPENAI_API_KEY` + `OPENAI_BASE_URL` | `ANTHROPIC_AUTH_TOKEN` + `ANTHROPIC_BASE_URL` |

> The gateway speaks both protocols — **one account, one key** runs both Codex and Claude Code.

## 2. Endpoint

| Field | Value |
| --- | --- |
| Protocol | OpenAI Responses / Chat Completions API |
| Base URL | `https://api.novadiffusion.com/v1` (you **must** append `/v1`) |
| Env vars | `OPENAI_API_KEY` + `OPENAI_BASE_URL` |
| API key | Copy from the console's Tokens page, shared with Claude Code |

> Codex uses the OpenAI protocol, so the Base URL **must** end in `/v1` — unlike Claude Code. Forgetting it causes 404s.

## 3. Install Codex CLI

Codex CLI requires **Node.js v22+**.

<div data-tabs="install-codex">
<div data-tab="macOS">

```bash
# install Node 22 via Homebrew (or download the LTS from nodejs.org)
brew install node@22

# install Codex CLI
npm install -g @openai/codex
codex --version
```

</div>
<div data-tab="Windows">

OpenAI currently recommends running Codex CLI under WSL2 on Windows:

```powershell
# install WSL (reboots once)
wsl --install
```

After rebooting, open Ubuntu and install Node.js v22 + Codex CLI:

```bash
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt install -y nodejs
npm install -g @openai/codex
codex --version
```

> If you hit shell / PTY / permission issues in native PowerShell, switch to WSL2.

</div>
<div data-tab="Linux">

```bash
# install Node.js v22
sudo apt update
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt install -y nodejs

# install Codex CLI
npm install -g @openai/codex
codex --version
```

</div>
</div>

## 4. Point it at the gateway (recommended: config file)

Use Codex CLI's official persistent files: `~/.codex/config.toml` (model + Base URL) and `~/.codex/auth.json` (key).

<div data-tabs="config-codex">
<div data-tab="macOS">

```bash
mkdir -p ~/.codex
nano ~/.codex/config.toml
```

Add:

```toml
model_provider = "hypitoken"
model = "gpt-5.6-sol"
model_reasoning_effort = "high"     # high / medium / low / minimal
disable_response_storage = true     # third-party gateways cannot store responses

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

Then write `auth.json`:

```bash
cat > ~/.codex/auth.json <<'JSON'
{
  "OPENAI_API_KEY": "YOUR_TOKEN_HERE"
}
JSON
chmod 600 ~/.codex/auth.json
```

</div>
<div data-tab="Windows">

In WSL2 (recommended) the steps match macOS / Linux. For native PowerShell the config dir is `%USERPROFILE%\.codex`:

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.codex"
notepad "$env:USERPROFILE\.codex\config.toml"
```

`config.toml`:

```toml
model_provider = "hypitoken"
model = "gpt-5.6-sol"
model_reasoning_effort = "high"     # high / medium / low / minimal
disable_response_storage = true     # third-party gateways cannot store responses

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

Then `auth.json`:

```powershell
@'
{
  "OPENAI_API_KEY": "YOUR_TOKEN_HERE"
}
'@ | Set-Content "$env:USERPROFILE\.codex\auth.json"
```

</div>
<div data-tab="Linux">

```bash
mkdir -p ~/.codex
nano ~/.codex/config.toml
```

Add:

```toml
model_provider = "hypitoken"
model = "gpt-5.6-sol"
model_reasoning_effort = "high"     # high / medium / low / minimal
disable_response_storage = true     # third-party gateways cannot store responses

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

Then write `auth.json`:

```bash
cat > ~/.codex/auth.json <<'JSON'
{
  "OPENAI_API_KEY": "YOUR_TOKEN_HERE"
}
JSON
chmod 600 ~/.codex/auth.json
```

</div>
</div>

### What the four required keys do

| Key | Why it matters |
| --- | --- |
| `model` | Default model. Without it Codex uses its own built-in default, which may not be in the gateway's available set. |
| `model_reasoning_effort` | Reasoning depth — see the table below. |
| `disable_response_storage` | Must be `true`. Third-party gateways don't offer OpenAI's response storage, and leaving it on makes requests fail. |
| `requires_openai_auth` | Must be `true`, otherwise Codex never sends the key from `auth.json` and you get a 401. |

`model_reasoning_effort` values:

| Value | When to use it |
| --- | --- |
| `high` | Architecture work, cross-file refactors, hard bugs. Slowest and priciest, highest success rate. |
| `medium` | The default for everyday coding and bug fixing. |
| `low` | Small edits, formatting, comments — when you want the answer fast. |
| `minimal` | Barely reasons at all; cheapest for mechanical transforms (translating, renaming, filling templates). |

> Higher effort means more thinking tokens, which cost more. When in doubt start at `medium`.

### Temporary debugging (env vars)

<div data-tabs="env-codex">
<div data-tab="macOS">

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="YOUR_TOKEN_HERE"
```

</div>
<div data-tab="Windows">

In WSL2:

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="YOUR_TOKEN_HERE"
```

Native PowerShell:

```powershell
$env:OPENAI_BASE_URL = "https://api.novadiffusion.com/v1"
$env:OPENAI_API_KEY = "YOUR_TOKEN_HERE"
```

</div>
<div data-tab="Linux">

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="YOUR_TOKEN_HERE"
```

</div>
</div>

## 5. Verify and run

**Check 1: is Node v22+?** `node -v` should print `v22` or higher. Codex needs a recent Node; v18 / v20 won't run it. Use `nvm install 22 && nvm use 22` to switch.

**Check 2: is Codex installed?** `codex --version` should print a version.

**Check 3: launch Codex.** Open a new terminal:

```bash
codex
```

In the interactive UI, test with:

```text
write a Python script that lists every filename in the current directory
```

## 6. Available models

OpenAI-side model IDs that currently have pricing:

```
gpt-5.2  gpt-5.3-codex  gpt-5.3-codex-spark
gpt-5.4  gpt-5.4-mini  gpt-5.5
gpt-5.6-sol  gpt-5.6-terra  gpt-5.6-luna
gpt-5  gpt-5-mini  gpt-5-nano  gpt-4o  gpt-4o-mini
```

`GET /v1/models` returns the **live set** actually available (it moves with the upstream plan); the console's model list is authoritative.

```bash
codex --model gpt-5.3-codex "optimise this code"
```

> Model names may carry a suffix and still bill correctly, e.g. `gpt-5.3-codex(high)`.

## 7. Using it headlessly

`codex exec` is the non-interactive mode: it takes one task description, does the work, and exits without opening the TUI. That is what you want in scripts, CI, cron jobs, or a higher-level agent.

```bash
# one-shot task, exits when done
codex exec "update the install steps in README to Node 22"

# pick a model
codex exec --model gpt-5.3-codex "add JSDoc to every exported function"
```

Config still comes from `~/.codex/config.toml` + `~/.codex/auth.json`; in a CI box that has neither, just set `OPENAI_BASE_URL` and `OPENAI_API_KEY` as environment variables.

## 8. Troubleshooting

**❌ 401 Unauthorized / Invalid API Key** — check the key is complete (with `sk-cpa-` prefix), has no spaces/newlines, and that you have balance.

**❌ 404 Not Found / model does not exist** — the Base URL is missing `/v1`, or the model name is wrong (per console).

**❌ `codex: command not found`** — the npm global bin isn't on PATH:

```bash
echo 'export PATH="$(npm config get prefix)/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**❌ Node.js version too low** — Codex needs v22+. Upgrade with `nvm install 22 && nvm use 22`.

**❌ 503 Service Unavailable** — the upstream pool temporarily has no usable credential. The body names the actual reason and the `Retry-After` header gives a suggested wait (capped at 300 seconds). Wait and retry, or check the [status page](/status).

**❌ 402 Payment Required** — out of balance. [Top up](/docs/top-up) in the console.

Quick reference:

| Code | What it actually means |
| --- | --- |
| `401` | Key missing / mistyped / has stray whitespace; or `requires_openai_auth` isn't `true` |
| `402` | Out of balance |
| `404` | `base_url` is missing `/v1`, or the model name doesn't exist |
| `503` | Upstream pool temporarily has no credential; `Retry-After` is at most 300 seconds |

## 9. Direct API

The endpoint supports both `/v1/chat/completions` and OpenAI's `/v1/responses`. Send the OpenAI request shape and get the OpenAI response shape — existing tooling works unchanged.

> The Codex side has **no User-Agent restriction** — curl, the official openai SDK, LiteLLM and friends can all call it directly. Use these two endpoints for scripting and automation rather than the Claude-side `/v1/messages`, which does filter clients (see [Claude Code setup](/docs/claude-code)).
