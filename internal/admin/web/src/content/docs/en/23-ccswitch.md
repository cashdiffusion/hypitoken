---
slug: ccswitch
title: CCSwitch credential manager
group: Tools
order: 23
intro: CCSwitch is a local credential / config manager that manages the provider config for Claude Code and Codex and switches in one click — no manual env-var edits. It isn't a gateway client itself; Claude Code / Codex still make the actual requests, so the matching CLI must be installed first.
---

## Before you start

- Registered, created a key, and topped up ([quick start](/docs/quick-start))
- **Claude Code already installed locally**, `claude --version` works in the terminal ([how to install](/docs/claude-code))
- Can download CCSwitch from GitHub or its website

> **CCSwitch is not a standalone client.** It manages Claude Code's configuration — switching providers in one click instead of editing env vars by hand. So Claude Code must already be installed.

## 1. About CCSwitch

CCSwitch is a desktop tool for switching quickly between Claude / AI providers. It automatically manages the config Claude Code needs (`ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN`), so you can switch providers without touching env vars.

| Field | Value |
| --- | --- |
| Protocol | Anthropic (passed through to Claude Code) |
| Base URL | `https://api.novadiffusion.com` (no `/v1`) |
| Requires | Claude Code installed locally |

## 2. Download and install

Download the installer for your system from CCSwitch's website or GitHub Releases.

<div data-tabs="install-ccswitch">
<div data-tab="macOS">

Download the `.dmg` and drag it into Applications. First launch asks for access to the `~/.claude` directory — if you denied it, re-enable under System Settings → Privacy & Security → Files and Folders.

</div>
<div data-tab="Windows">

Download the `.exe` and install. CCSwitch auto-detects your local Claude Code on launch.

</div>
<div data-tab="Linux">

Download the AppImage:

```bash
chmod +x ccswitch-*.AppImage
./ccswitch-*.AppImage
```

Or install a `.deb` / `.rpm` package.

</div>
</div>

## 3. Add HypiToken as a provider

1. Launch CCSwitch, choose **Key management** on the left — you'll see the configured providers (official Claude by default).
2. Click **Add** and fill in:
   - **Name**: `HypiToken`
   - **Base URL**: `https://api.novadiffusion.com`
   - **API Key**: `sk-cpa-...` (your gateway key)
3. Save — it appears in the provider list.

## 4. Switch to HypiToken in one click

In Key management, click the "⋯" (three dots) on the HypiToken row and choose **Switch CC**. Claude Code switches to HypiToken — no restart of CCSwitch needed.

> **What it does under the hood.** "Switch CC" rewrites Claude Code's `~/.claude/settings.json`, setting `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` for you. The next `claude` you run uses the gateway.

## 5. Troubleshooting

**❌ `claude` still hits the official API after switching** — ① you didn't reopen the terminal, so the config isn't loaded; ② your shell rc (`.zshrc` / `.bashrc`) hard-codes `ANTHROPIC_BASE_URL`, which overrides CCSwitch — remove that line and retry.

**❌ CCSwitch can't find Claude Code** — install Claude Code first. Follow the [Claude Code guide](/docs/claude-code) to get the `claude` command, then come back.

**❌ macOS authorization fails on launch** — first launch needs access to `~/.claude`. Re-enable under System Settings → Privacy & Security → Files and Folders.
