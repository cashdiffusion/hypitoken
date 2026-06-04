---
slug: claude-code
title: Claude Code 接入
group: 客户端接入
order: 20
intro: 30 秒把 Anthropic 官方 Claude Code CLI 指向网关。Advisor、深度思考、子 agent、MCP 均按原生体验使用。
---

## 开始之前请确认

- 已注册 HypiToken 账号（[如何注册](/docs/register)）
- 已创建 API 密钥并**已复制完整 `sk-cpa-...` 到剪贴板**（[如何创建](/docs/create-key)）
- 账户有余额或仍有新人试用额度（[如何充值](/docs/top-up)）
- 知道怎么打开命令行（[没用过命令行？点这里](/docs/beginners)）

## 一、端点信息

| 项 | 值 |
| --- | --- |
| 协议 | Anthropic Messages API |
| Base URL | `https://api.novadiffusion.com`（末尾**不要**加 `/v1`） |
| 环境变量 | `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` |
| API Key | 从控制台「密钥管理」复制，格式 `sk-cpa-...` |

> Base URL **不要**加 `/v1`。Anthropic 协议跟 OpenAI 协议不同，加了反而会 404。

## 二、安装 Claude Code

右上角的「系统」开关选你的系统，下面的命令会跟着切换。

<div data-tabs="install-cc">
<div data-tab="macOS">

```bash
# 官方原生安装器（推荐）
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
# 官方原生安装器（推荐）
irm https://claude.ai/install.ps1 | iex

# 或使用 WinGet
winget install Anthropic.ClaudeCode

# 验证
claude --version
```

> 不再需要为了 Claude Code 单独装 WSL2，原生 PowerShell 就够。只有当你自己的开发工具链依赖 Linux 环境时才用 WSL2。

</div>
<div data-tab="Linux">

```bash
# 官方原生安装器（推荐）
curl -fsSL https://claude.ai/install.sh | bash

# 验证
claude --version
```

</div>
</div>

## 三、指向网关（推荐：配置文件）

推荐把网关地址和 Key 写进 Claude Code 官方的 `settings.json`，`env` 下的变量会应用到每个会话。

<div data-tabs="config-cc">
<div data-tab="macOS">

```bash
mkdir -p ~/.claude
nano ~/.claude/settings.json
```

填入（把 `YOUR_TOKEN_HERE` 换成你的真密钥）：

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

填入：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "YOUR_TOKEN_HERE"
  }
}
```

保存后**重新打开 PowerShell** 再运行 `claude`。

</div>
<div data-tab="Linux">

```bash
mkdir -p ~/.claude
nano ~/.claude/settings.json
```

填入：

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

> `ANTHROPIC_AUTH_TOKEN` 会让 Claude Code 发送 `Authorization: Bearer <token>`，正是网关需要的形式。

### 临时调试（环境变量）

只想跑一次测试，可以在当前终端临时设置环境变量：

<div data-tabs="env-cc">
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

> 临时变量只在当前终端窗口有效，关闭后失效。长期使用请用上面的 `settings.json` 方式。

## 四、验证接入

动手跑 `claude` 之前，先逐项对照下面 3 个检查点。

**检查点 1：Claude Code 装好了吗？** 执行 `claude --version`，应输出版本号。
失败：`command not found` → 重开命令行窗口；Windows 确认安装时勾选了「Add to PATH」。

**检查点 2：配置生效了吗？** 确认 `~/.claude/settings.json`（Windows 为 `%USERPROFILE%\.claude\settings.json`）里 `ANTHROPIC_BASE_URL` 拼写正确、是 `https://api.novadiffusion.com`（无 `/v1`）。

**检查点 3：启动 Claude Code。** 新开一个命令行窗口，进入任意项目目录：

```bash
claude
```

看到交互界面后，输入一句话测试：

```text
帮我写一个 hello world 的 Python 脚本
```

能正常流式返回，就说明接入成功 🎉。

## 五、运行

```bash
# 交互式会话
claude

# 一次性提问
claude "总结一下这个 diff"

# Advisor 模式（Claude Code ≥ 2.1）
claude --advisor "审查我的架构"
```

## 六、支持的特性

| 特性 | 状态 |
| --- | --- |
| Advisor 模式 | ✓ |
| 深度思考 Extended thinking | ✓ |
| 子 agent | ✓ |
| MCP 工具调用 | ✓ |
| 提示缓存 Prompt caching | ✓ |
| 流式响应 | ✓ |

可用模型（以控制台为准）：`claude-haiku-4-5`（最快）、`claude-sonnet-4-6`（均衡推荐）、`claude-opus-4-7`（最强）。

## 七、常见报错排查

**❌ 401 Unauthorized / 认证失败** — Key 填写有误。检查是否完整复制（含 `sk-cpa-` 前缀）、有无多余空格、在控制台是否仍有效。

**❌ 404 Not Found** — Base URL 配置错误。正确格式 `https://api.novadiffusion.com`（结尾不要加 `/` 也不要加 `/v1`）。

**❌ 请求一直超时 / 很慢** — 稍等片刻再试，或查看 [状态页](/status)。也可能是本地网络问题（VPN、防火墙）。

**❌ `claude: command not found`** — npm 全局 bin 没加进 PATH。macOS / Linux：

```bash
echo 'export PATH="$(npm config get prefix)/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

Windows：以管理员身份重新打开 PowerShell，或重装时勾选「Add to PATH」。

## 八、路由原理

网关为每个客户端 Token 维护**粘性会话**：你的 Claude Code 会话在 10 分钟活跃窗口内落在同一个上游凭证上，保留多轮缓存命中、保持对话连续。若该凭证不可用，池管理器会自动切换到下一个健康凭证并重试。
