---
slug: self-host
title: 自托管部署
group: 运维
order: 40
intro: 自己组织或团队从源码运行 HypiToken。
---

## 前置依赖

- **Go ≥ 1.24**
- **Bun** (前端构建)
- *可选:* SMTP 服务器 (邮箱验证)

## 构建

```bash
git clone https://github.com/cashdiffusion/hypitoken.git
cd hypitoken
make build
# 产物 bin/cpa-claude
```

## 配置

复制 `config.example.yaml` 为 `config.yaml`,把 `saas.enabled` 设为 `true`,填好 `admin_email` / `admin_password` 用于首次启动时引导管理员账户。

```yaml
auth_dir: /var/lib/hypitoken/auths   # 凭证文件目录
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

## 运行

```bash
./bin/cpa-claude -config config.yaml
# 监听:
#   :8317  Claude  (主端点,SaaS 站点挂在 /)
#   :8318  Codex
```

## 反向代理

前面挂 Caddy 或 nginx 做按路径的路由,Caddy 示例:

```
api.example.com {
  handle /v1/chat/* { reverse_proxy localhost:8318 }
  handle /v1/responses* { reverse_proxy localhost:8318 }
  handle /v1/models { reverse_proxy localhost:8318 }
  handle { reverse_proxy localhost:8317 }
}
```
