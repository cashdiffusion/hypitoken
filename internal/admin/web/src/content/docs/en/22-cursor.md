---
slug: cursor
title: Cursor setup
group: Clients
order: 22
intro: Cursor is an AI code editor built on VS Code. Connect it via OpenAI-compatible mode (Override) by filling a few fields — no command line at all.
---

## Before you start

- Registered and created a key ([quick start](/docs/quick-start))
- Have balance or remaining trial credit ([how to top up](/docs/top-up))
- Your machine can reach the internet (the Cursor installer is sizeable)

> **Beginner-friendly.** Cursor is a GUI — no terminal, just clicks and a few input fields. If the command line put you off, start here.

## 1. Endpoint

| Field | Value |
| --- | --- |
| Protocol | OpenAI Chat Completions API |
| OpenAI Base URL | `https://api.novadiffusion.com/v1` |
| API key | Copy from the console's Tokens page, format `sk-cpa-...` |

> This guide uses **OpenAI-compatible mode (Override)**: fill in the Base URL + key to override the official endpoint, and every OpenAI-family model the gateway supports becomes available. The Base URL **must** end in `/v1`.

## 2. Download and install

Download the installer for your system from [cursor.com](https://www.cursor.com).

<div data-tabs="install-cursor">
<div data-tab="macOS">

Download the `.dmg` and drag it into Applications. If first launch warns about an unidentified developer, go to System Settings → Privacy & Security and click "Open Anyway."

</div>
<div data-tab="Windows">

Download the `.exe` installer and follow the wizard. Launch Cursor from the Start menu afterwards.

</div>
<div data-tab="Linux">

Download the AppImage:

```bash
chmod +x cursor-*.AppImage
./cursor-*.AppImage
```

If it crashes on launch, try `./cursor-*.AppImage --no-sandbox`.

</div>
</div>

## 3. Configure the gateway in Cursor

### 1. Open settings

- Menu: `File → Preferences → Cursor Settings`
- Shortcut: Windows / Linux `Ctrl + Shift + J`; macOS `Cmd + Shift + J`
- Or the gear icon ⚙️ → `Cursor Settings`

### 2. Fill in OpenAI API Key (Override)

In Cursor Settings, click **Models** on the left, scroll to the bottom to the **OpenAI API Key** section, and fill in:

| Field | Value |
| --- | --- |
| API Key | `sk-cpa-...` (your gateway key) |
| Base URL (Override) | `https://api.novadiffusion.com/v1` |

Click **Verify** — Cursor sends a test request to check the connection.

```text
Cursor Settings
└── Models
    ├── model list (tick the ones to enable)
    └── OpenAI API Key (scroll to bottom)
        ├── API Key:  [ sk-cpa-...                          ]
        ├── Base URL: [ https://api.novadiffusion.com/v1    ]
        └── [ Verify ] ← click here
```

> **What does success look like?** A green check or "Verified" next to the button.
> If it fails: ① is `/v1` on the end of the Base URL (most common mistake); ② any stray spaces/newlines in the key (re-copy from the console); ③ enough balance; ④ try a different network.

## 4. Pick a model and verify

After the key is set, tick the models you want in **Models**. Then press `Ctrl + L` (macOS: `Cmd + L`) to open the chat, pick your enabled model from the top-right dropdown, and test:

```text
hi, write me a Hello World Python script
```

Streaming code within a few seconds means it works.

> Available model names follow the console's [Pricing](/docs/pricing) page. Cursor's Tab completion relies on its proprietary completion model and may not use your custom Base URL — focus on the Chat features.

## 5. Troubleshooting

**❌ Verify turns red / fails** — ① Base URL missing `/v1`: it should be `https://api.novadiffusion.com/v1`; ② key copied incompletely (check the `sk-cpa-` prefix); ③ insufficient balance.

**❌ Chat hangs / spins forever** — network issue, retry; or the chosen model isn't supported, pick another.

**❌ Model not found** — wrong model name; use the console's supported list, minding case and hyphens.
