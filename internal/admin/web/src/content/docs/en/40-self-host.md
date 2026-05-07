---
slug: self-host
title: Self-hosting
group: Operator
order: 40
intro: Run HypiToken from source for your own organization or team.
---

## Requirements

- **Go ≥ 1.24**
- **Bun** (frontend build)
- *Optional:* SMTP server for email verification

## Build

### macOS

```bash
git clone https://github.com/cashdiffusion/hypitoken.git
cd hypitoken
make build
# binary at bin/hypitoken
```

### Windows

For local server builds on Windows, use Windows 11 + WSL2. Install WSL from PowerShell:

```powershell
wsl --install
```

After rebooting, open Ubuntu, install Go and Bun, then run:

```bash
git clone https://github.com/cashdiffusion/hypitoken.git
cd hypitoken
make build
# binary at bin/hypitoken
```

## Configure

Copy `config.example.yaml` to `config.yaml`. Set `saas.enabled: true` and fill in `admin_email` / `admin_password`; the first start uses them to create the admin account.

```yaml
auth_dir: /var/lib/hypitoken/auths   # credential files live here
log_dir:  /var/lib/hypitoken/requests

endpoints:
  claude: { host: 0.0.0.0, port: 8317 }
  codex:  { host: 0.0.0.0, port: 8318 }

saas:
  enabled: true
  db_path: ./saas.db
  admin_email: you@example.com
  admin_password: a-strong-password
  smtp:
    host: smtp.example.com
    port: 587
    username: postmaster
    password: ${SMTP_PASS}
    from: noreply@example.com
    use_tls: true
```

## Run

### macOS / Linux / WSL2

```bash
./bin/hypitoken -config config.yaml
# Listening on:
#   :8317  Claude  (primary, hosts the SaaS site at /)
#   :8318  Codex
```

### Windows PowerShell

If you build a Windows binary, run it from PowerShell:

```powershell
.\bin\hypitoken.exe -config config.yaml
```

For production, Linux or WSL2 is still recommended because systemd, reverse proxies, and certificate automation are easier to operate there.

## Reverse proxy

Put Caddy or nginx in front. Example Caddy config for path-based routing:

```
api.example.com {
  handle /v1/chat/* { reverse_proxy localhost:8318 }
  handle /v1/responses* { reverse_proxy localhost:8318 }
  handle /v1/models { reverse_proxy localhost:8318 }
  handle { reverse_proxy localhost:8317 }
}
```
