---
slug: claude-code
title: Claude Code 接入
group: 客户端
order: 10
intro: 30 秒把官方 Claude Code CLI 指向网关。Advisor、深度思考、子 agent 均可按原生体验使用。
---

## 安装 Claude Code

<div data-tabs="os-install-cc">
<div data-tab="macOS">

```bash
# 官方原生安装器
curl -fsSL https://claude.ai/install.sh | bash

# 或使用 Homebrew
brew install --cask claude-code

# 验证
claude --version
```

</div>
<div data-tab="Windows">

Claude Code 官方支持 Windows 原生运行。建议先安装 [Git for Windows](https://git-scm.com/downloads/win)，然后在 PowerShell 中安装：

```powershell
# 官方原生安装器
irm https://claude.ai/install.ps1 | iex

# 或使用 WinGet
winget install Anthropic.ClaudeCode

# 验证
claude --version
```

不再需要为了 Claude Code 单独安装 WSL2。只有当你自己的工具链依赖 Linux 环境时，才建议使用 WSL2。

</div>
<div data-tab="Linux">

```bash
# 官方原生安装器
curl -fsSL https://claude.ai/install.sh | bash

# 验证
claude --version
```

</div>
</div>

## 指向网关

推荐把网关地址和令牌写入 Claude Code 的官方 `settings.json`。`env` 中的环境变量会应用到每个会话。

<div data-tabs="os-config-cc">
<div data-tab="macOS">

```bash
mkdir -p ~/.claude
nano ~/.claude/settings.json
```

写入：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "YOUR_TOKEN_HERE"
  }
}
```

</div>
<div data-tab="Windows">

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.claude"
notepad "$env:USERPROFILE\.claude\settings.json"
```

写入同样的 JSON：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "YOUR_TOKEN_HERE"
  }
}
```

保存后重新打开 PowerShell，再运行 `claude`。

</div>
<div data-tab="Linux">

```bash
mkdir -p ~/.claude
nano ~/.claude/settings.json
```

写入：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "YOUR_TOKEN_HERE"
  }
}
```

</div>
</div>

> **`ANTHROPIC_AUTH_TOKEN`** 是官方 Claude Code OAuth 流程内部使用的变量。设置后，Claude Code 会发送 `Authorization: Bearer <token>`，这正是网关期望的鉴权格式。

### 临时调试

如果只想临时测试，也可以在当前 shell 中设置环境变量：

<div data-tabs="os-env-cc">
<div data-tab="macOS">

```bash
export ANTHROPIC_BASE_URL="https://api.novadiffusion.com"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKEN_HERE"
```

</div>
<div data-tab="Windows">

```powershell
$env:ANTHROPIC_BASE_URL = "https://api.novadiffusion.com"
$env:ANTHROPIC_AUTH_TOKEN = "YOUR_TOKEN_HERE"
```

</div>
<div data-tab="Linux">

```bash
export ANTHROPIC_BASE_URL="https://api.novadiffusion.com"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKEN_HERE"
```

</div>
</div>

## 运行

```bash
# 交互式会话
claude

# 一次性问题
claude "总结这个 diff"

# Advisor 模式 (Claude Code ≥ 2.1)
claude --advisor "审查我的架构"
```

## 支持的特性

Claude Code 的核心能力通过网关可直接使用，无需额外配置：

| 特性 | 状态 |
| --- | --- |
| Advisor 模式 | ✓ |
| 深度思考 | ✓ |
| 子 agent | ✓ |
| MCP 工具调用 | ✓ |
| 提示词缓存 | ✓ |
| 流式响应 | ✓ |

## 可用模型

- `claude-haiku-4-5-20251001` — 最快
- `claude-sonnet-4-6` — 均衡 (推荐)
- `claude-opus-4-7` — 最强

## 路由原理

网关为每个客户端令牌维护**粘性会话**：你的 Claude Code 会话在 10 分钟活跃窗口内会固定落到同一个上游凭证，从而保留多轮缓存命中和会话连续性。

如果该凭证不可用，池管理器会自动切换到下一个健康凭证，并重试当前请求。
