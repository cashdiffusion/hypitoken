---
slug: self-host
title: 自托管部署
group: 运维
order: 40
intro: 为自己的组织或团队从源码运行 HypiToken。
---

## 前置依赖

- **Go ≥ 1.24**
- **Bun**（前端构建）
- *可选：* SMTP 服务器（邮箱验证）

## 构建

### macOS

```bash
git clone https://github.com/cashdiffusion/hypitoken.git
cd hypitoken
make build
# 产物 bin/hypitoken
```

### Windows

推荐在 Windows 11 + WSL2 中构建和运行服务端。PowerShell 里先安装 WSL：

```powershell
wsl --install
```

重启后打开 Ubuntu，安装 Go 与 Bun，再执行：

```bash
git clone https://github.com/cashdiffusion/hypitoken.git
cd hypitoken
make build
# 产物 bin/hypitoken
```

## 配置

复制 `config.example.yaml` 为 `config.yaml`，把 `saas.enabled` 设为 `true`，并填写 `admin_email` / `admin_password`。首次启动时系统会用这组信息创建管理员账户。

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

### macOS / Linux / WSL2

```bash
./bin/hypitoken -config config.yaml
# 监听:
#   :8317  Claude  (主端点，SaaS 站点挂在 /)
#   :8318  Codex
```

### Windows PowerShell

如果你编译了 Windows 二进制，可在 PowerShell 中运行：

```powershell
.\bin\hypitoken.exe -config config.yaml
```

生产部署仍建议使用 Linux 服务器或 WSL2 环境，便于 systemd、反向代理和证书管理。

## 反向代理

建议在前面挂 Caddy 或 nginx 做按路径路由。Caddy 示例：

```
api.example.com {
  handle /v1/chat/* { reverse_proxy localhost:8318 }
  handle /v1/responses* { reverse_proxy localhost:8318 }
  handle /v1/models { reverse_proxy localhost:8318 }
  handle { reverse_proxy localhost:8317 }
}
```
