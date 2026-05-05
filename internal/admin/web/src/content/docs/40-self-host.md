---
slug: self-host
title: Self-hosting
group: Operator
order: 40
intro: Run HypiToken from source for your own organisation or team.
---

## Requirements

- **Go ≥ 1.24**
- **Bun** (frontend build)
- *Optional:* SMTP server for email verification

## Build

```bash
git clone https://github.com/cashdiffusion/hypitoken.git
cd hypitoken
make build
# binary at bin/cpa-claude
```

## Configure

Copy `config.example.yaml` to `config.yaml`. Set `saas.enabled: true` and fill in `admin_email` / `admin_password` to bootstrap the first admin user.

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

```bash
./bin/cpa-claude -config config.yaml
# Listening on:
#   :8317  Claude  (primary, hosts SaaS site at /)
#   :8318  Codex
```

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
