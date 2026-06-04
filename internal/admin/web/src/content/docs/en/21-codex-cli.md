---
slug: codex-cli
title: Codex CLI setup
group: Clients
order: 21
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
| Env vars | `OPENAI_API_KEY` + `OPENAI_BASE_URL` | `ANTHROPIC_API_KEY` + `ANTHROPIC_BASE_URL` |

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

Or run non-interactively:

```bash
# pass a task directly
codex "explain what files are in this directory"

# pick a model
codex --model gpt-5.5 "optimise this code"
```

## 6. Troubleshooting

**❌ 401 Unauthorized / Invalid API Key** — check the key is complete (with `sk-cpa-` prefix), has no spaces/newlines, and that you have balance.

**❌ 404 Not Found / model does not exist** — the Base URL is missing `/v1`, or the model name is wrong (per console).

**❌ `codex: command not found`** — the npm global bin isn't on PATH:

```bash
echo 'export PATH="$(npm config get prefix)/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**❌ Node.js version too low** — Codex needs v22+. Upgrade with `nvm install 22 && nvm use 22`.

## 7. Direct API

The endpoint supports both `/v1/chat/completions` and OpenAI's `/v1/responses`. Send the OpenAI request shape and get the OpenAI response shape — existing tooling works unchanged.
