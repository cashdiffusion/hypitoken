---
slug: codex-cli
title: Codex CLI setup
group: Clients
order: 20
intro: OpenAI's Codex CLI connects to the unified gateway through `/v1`.
---

## Install Codex CLI

<div data-tabs="os-install-codex">
<div data-tab="macOS">

```bash
npm install -g @openai/codex

# verify
codex --version
```

</div>
<div data-tab="Windows">

OpenAI's current guidance recommends Windows 11 users run Codex CLI through WSL2:

```powershell
# PowerShell
wsl --install
```

After rebooting, open Ubuntu, install Node.js / npm, then install Codex CLI inside WSL:

```bash
npm install -g @openai/codex
codex --version
```

If you hit shell, PTY, or permission issues in native PowerShell, switch to WSL2.

</div>
<div data-tab="Linux">

```bash
npm install -g @openai/codex

# verify
codex --version
```

</div>
</div>

## Point it at the gateway

Use Codex CLI's official persistent configuration files:

- `~/.codex/config.toml` — configures the model provider and base URL
- `~/.codex/auth.json` — stores the API key

<div data-tabs="os-config-codex">
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

In WSL2 (recommended):

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

For native PowerShell, the config directory is `%USERPROFILE%\.codex`:

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.codex"
notepad "$env:USERPROFILE\.codex\config.toml"
```

Then create `auth.json`:

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

### Temporary debugging

For a one-off test, environment variables still work:

<div data-tabs="os-env-codex">
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

In native PowerShell:

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

> **Same key, same host.** Claude Code and Codex share the gateway domain and your API token. The gateway routes `/v1/chat/*`, `/v1/responses*`, and `/v1/models` to the OpenAI-compatible endpoint; other requests go to Claude.

## Run it

```bash
# interactive Codex session
codex

# one-shot
codex "explain this stack trace"
```

## Direct API

The endpoint supports both `/v1/chat/completions` and OpenAI's `/v1/responses`. Send the OpenAI request shape and receive the OpenAI response shape; existing tooling works unchanged.
