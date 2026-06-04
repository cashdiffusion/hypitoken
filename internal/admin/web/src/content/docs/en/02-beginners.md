---
slug: beginners
title: First-timers
group: Getting started
order: 2
intro: First time with APIs, the command line, or environment variables? Three minutes here makes every client guide that follows 10× smoother.
---

## 1. The vocabulary, one line each

| Term | In one sentence |
| --- | --- |
| **API** | The interface programs talk to each other through. Your client talks to Claude / GPT servers over an API. |
| **API Key** | A string that acts like an ID card — the client uses it to prove "I'm a paying user." Format here is `sk-cpa-xxxx`; keep it secret, never commit it to Git. |
| **Base URL** | The endpoint address telling the client "where to call." Here it's `https://api.novadiffusion.com`. |
| **Environment variable** | A system-wide setting CLI tools read on startup. This guide mainly uses `*_API_KEY` and `*_BASE_URL`. |
| **Command line / terminal / CMD** | Three names for the same thing: a window where you type text commands. Opening it is covered below. |
| **npm** | The "package store command" installed alongside Node.js. Many commands here start with `npm` to install / remove tools. |

## 2. How to open a terminal (essential)

Pick your system with the **System** switch (top right) — the whole guide follows your choice. Remember how to open it; every command below runs there.

<div data-tabs="open-terminal">
<div data-tab="macOS">

**Open the Terminal app**

- Press `Command (⌘) + Space` for Spotlight, type `Terminal`, hit Enter.
- Or find it under Finder → Applications → Utilities → Terminal.

A prompt like `yourname@MacBook ~ %` means you're in. Commands go here.

> **Copy / paste**: `⌘ + C` / `⌘ + V` as usual.

</div>
<div data-tab="Windows">

**Open PowerShell**

- Press `Win`, type `PowerShell`, click "Windows PowerShell."
- For admin rights, right-click and choose "Run as administrator."

A prompt like `PS C:\Users\yourname>` means you're in.

> **Copy / paste**: copy with `Ctrl + C`; in PowerShell, **right-click** to paste (or `Ctrl + V`).

</div>
<div data-tab="Linux">

**Open the Terminal**

- Most distros: `Ctrl + Alt + T`.
- Or search `Terminal` in the application menu.

A prompt like `user@host:~$` means you're in.

> **Paste**: `Ctrl + Shift + V` (the `Shift` matters — plain `Ctrl + V` does something else in a terminal).

</div>
</div>

## 3. Don't panic at these

**Placeholder `sk-cpa-xxxxxxxx`** — every `sk-cpa-xxxxxxxx` in this guide is fake. Replace the whole thing (including `sk-cpa-`) with the real key you copied from the console, e.g. `sk-cpa-abc123...`. Don't leave any `xxxxxxxx` behind.

**The `~` (tilde)** — in macOS / Linux terminals, `~` is your home directory. `cd ~/Desktop` goes to your Desktop.

**Leading `$` / `%` / `>`** — these are the terminal's prompt, not part of the command. This site's guides omit them, so what you see is exactly what you type.

**`command not found`** — the program isn't installed, or it is but the system can't find it. Each client guide's troubleshooting section has fixes; usually "restart the terminal" does it.

## 4. Which client should I pick?

| What you want | Recommended | Difficulty |
| --- | --- | --- |
| AI chat + completion inside an editor (best for beginners) | **Cursor** | ⭐ Easy |
| Write code in the terminal with Claude | **Claude Code** | ⭐⭐ Easy |
| Write code in the terminal with GPT | **Codex CLI** | ⭐⭐ Easy |
| Already have Claude Code, want to switch providers | **CCSwitch** | ⭐⭐ Beginner |
| No idea | Start with **Cursor** | ⭐ Easy |

> Cursor is a GUI — no terminal at all, just fill in a few fields after installing. Claude Code / Codex are CLI tools, but their guides walk you through step by step.
