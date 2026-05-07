---
slug: codex-cli
title: Codex CLI 接入
group: 客户端
order: 20
intro: OpenAI 官方 Codex CLI 可通过 `/v1` 接入统一网关。
---

## 安装 Codex CLI

<div data-tabs="os-install-codex">
<div data-tab="macOS">

```bash
npm install -g @openai/codex

# 验证
codex --version
```

</div>
<div data-tab="Windows">

OpenAI 官方安装说明当前建议 Windows 11 用户通过 WSL2 运行 Codex CLI：

```powershell
# PowerShell
wsl --install
```

重启后打开 Ubuntu 终端，在 WSL 内安装 Node.js / npm，再安装 Codex CLI：

```bash
npm install -g @openai/codex
codex --version
```

如果在 Windows 原生 PowerShell 中运行遇到 shell、PTY 或权限问题，请切换到 WSL2。

</div>
<div data-tab="Linux">

```bash
npm install -g @openai/codex

# 验证
codex --version
```

</div>
</div>

## 指向网关

推荐使用 Codex CLI 的官方持久化配置文件：

- `~/.codex/config.toml`：配置模型提供商和 base URL
- `~/.codex/auth.json`：保存 API key

<div data-tabs="os-config-codex">
<div data-tab="macOS">

```bash
mkdir -p ~/.codex
nano ~/.codex/config.toml
```

写入：

```toml
model_provider = "hypitoken"

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

然后写入 `auth.json`：

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

在 WSL2 中（推荐）：

```bash
mkdir -p ~/.codex
nano ~/.codex/config.toml
```

写入：

```toml
model_provider = "hypitoken"

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

然后写入 `auth.json`：

```bash
cat > ~/.codex/auth.json <<'JSON'
{
  "OPENAI_API_KEY": "YOUR_TOKEN_HERE"
}
JSON
chmod 600 ~/.codex/auth.json
```

在原生 PowerShell 中，配置目录在 `%USERPROFILE%\.codex`：

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.codex"
notepad "$env:USERPROFILE\.codex\config.toml"
```

然后创建 `auth.json`：

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

写入：

```toml
model_provider = "hypitoken"

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

然后写入 `auth.json`：

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

### 临时调试

临时测试时仍可使用环境变量：

<div data-tabs="os-env-codex">
<div data-tab="macOS">

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="YOUR_TOKEN_HERE"
```

</div>
<div data-tab="Windows">

在 WSL2 中：

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="YOUR_TOKEN_HERE"
```

在原生 PowerShell 中：

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

> **同一个令牌，同一个域名。** Claude Code 和 Codex 共用网关域名与 API 令牌。网关会把 `/v1/chat/*`、`/v1/responses*`、`/v1/models` 路由到 OpenAI 兼容端点，其余请求进入 Claude。

## 运行

```bash
# 交互式 Codex 会话
codex

# 一次性
codex "解释这条堆栈"
```

## 直接调 API

端点同时支持 `/v1/chat/completions` 和 OpenAI 的 `/v1/responses`。发送 OpenAI 请求格式，接收 OpenAI 响应格式，现有工具链无需改造。
