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
- *Optional:* Alipay merchant account (mock gateway in dev)

## Build

```bash
git clone https://github.com/wjsoj/CPA-Claude.git
cd CPA-Claude
make build
# binary at bin/cpa-claude
```

## Configure

Copy `config.example.yaml` to `config.yaml`. Set `saas.enabled: true` and
fill in `admin_email` / `admin_password` to bootstrap the first admin user.

```yaml
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
  alipay:
    app_id: ""        # empty = mock gateway (dev)
    private_key: "@/etc/cpa/alipay.key"
    alipay_public_key: "@/etc/cpa/alipay-public.key"
    is_production: true
    notify_url: https://your.host/api/v2/billing/notify
```

## Run

```bash
./bin/cpa-claude -config config.yaml
# Listening on:
#   :8317  Claude  (primary, hosts SaaS site at /)
#   :8318  Codex
# Legacy admin panel: /mgmt-console
# SaaS site:          /
```

> **Reverse proxy.** Put nginx or Cloudflare Tunnel in front. The Alipay
> webhook (`/api/v2/billing/notify`) needs to be publicly reachable.
