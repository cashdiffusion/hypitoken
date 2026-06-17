---
slug: codex-cli
title: Codex CLI 接入
group: 客户端接入
order: 21
intro: OpenAI 官方 Codex CLI 通过 /v1 接入统一网关。同一个 Key 与 Claude Code 通用。
---

## 开始之前请确认

- 已注册 HypiToken 账号并创建密钥（[快速上手](/docs/quick-start)）
- 账户有余额或仍有新人试用额度（[如何充值](/docs/top-up)）
- 知道怎么打开命令行（[没用过命令行？点这里](/docs/beginners)）

## 一、关于 Codex CLI

Codex CLI 是 OpenAI 官方出品的终端 AI 编程助手，可以在命令行里用自然语言操控代码、执行命令、读写文件。

| 对比项 | Codex CLI | Claude Code |
| --- | --- | --- |
| 出品方 | OpenAI | Anthropic |
| 接口协议 | OpenAI 格式 | Anthropic 格式 |
| Node 版本要求 | **v22+** | v18+ |
| 环境变量 | `OPENAI_API_KEY` + `OPENAI_BASE_URL` | `ANTHROPIC_API_KEY` + `ANTHROPIC_BASE_URL` |

> 网关同时支持两种协议，**一个账号、一个 Key** 可以同时用 Codex 和 Claude Code。

## 二、端点信息

| 项 | 值 |
| --- | --- |
| 协议 | OpenAI Responses / Chat Completions API |
| Base URL | `https://api.novadiffusion.com/v1`（末尾**必须**加 `/v1`） |
| 环境变量 | `OPENAI_API_KEY` + `OPENAI_BASE_URL` |
| API Key | 从控制台「密钥管理」复制，可与 Claude Code 共用 |

> Codex 使用 OpenAI 协议，Base URL 末尾**必须**加 `/v1`，这跟 Claude Code 不同。忘记加会 404。

## 三、安装 Codex CLI

Codex CLI 要求 **Node.js v22+**。

<div data-tabs="install-codex">
<div data-tab="macOS">

```bash
# 用 Homebrew 装 Node 22（或去 nodejs.org 下载 LTS）
brew install node@22

# 安装 Codex CLI
npm install -g @openai/codex
codex --version
```

</div>
<div data-tab="Windows">

OpenAI 当前建议 Windows 用户通过 WSL2 运行 Codex CLI：

```powershell
# 安装 WSL（首次需重启）
wsl --install
```

重启后进入 Ubuntu，安装 Node.js v22 与 Codex CLI：

```bash
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt install -y nodejs
npm install -g @openai/codex
codex --version
```

> 若在原生 PowerShell 遇到 shell / PTY / 权限问题，切到 WSL2 即可。

</div>
<div data-tab="Linux">

```bash
# 安装 Node.js v22
sudo apt update
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt install -y nodejs

# 安装 Codex CLI
npm install -g @openai/codex
codex --version
```

</div>
</div>

## 四、指向网关（推荐：配置文件）

使用 Codex CLI 官方持久化配置文件：`~/.codex/config.toml`（模型与 Base URL）+ `~/.codex/auth.json`（密钥）。

<div data-tabs="config-codex">
<div data-tab="macOS">

```bash
mkdir -p ~/.codex
nano ~/.codex/config.toml
```

填入：

```toml
model_provider = "hypitoken"

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
```

再写 `auth.json`：

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

在 WSL2 中（推荐）操作同 macOS / Linux。原生 PowerShell 的配置目录是 `%USERPROFILE%\.codex`：

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.codex"
notepad "$env:USERPROFILE\.codex\config.toml"
```

`config.toml` 内容：

```toml
model_provider = "hypitoken"

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
```

再写 `auth.json`：

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

填入：

```toml
model_provider = "hypitoken"

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
```

再写 `auth.json`：

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

### 临时调试（环境变量）

<div data-tabs="env-codex">
<div data-tab="macOS">

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="YOUR_TOKEN_HERE"
```

</div>
<div data-tab="Windows">

WSL2 中：

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="YOUR_TOKEN_HERE"
```

原生 PowerShell：

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

## 五、验证与运行

**检查点 1：Node 是 v22+ 吗？** `node -v` 应输出 `v22` 起步的版本。Codex 对 Node 版本要求较新，v18 / v20 跑不起来。可用 `nvm install 22 && nvm use 22` 切换。

**检查点 2：Codex 装好了吗？** `codex --version` 应输出版本号。

**检查点 3：启动 Codex。** 新开终端执行：

```bash
codex
```

进入交互界面后输入测试：

```text
帮我写一个读取当前目录所有文件名的 Python 脚本
```

也可以非交互式直接运行：

```bash
# 直接传入任务描述
codex "解释一下这个目录里有哪些文件"

# 指定模型
codex --model gpt-5.5 "帮我优化这段代码"
```

## 六、常见报错排查

**❌ 401 Unauthorized / Invalid API Key** — 检查 Key 是否完整（含 `sk-cpa-` 前缀）、有无空格或换行、余额是否充足。

**❌ 404 Not Found / model does not exist** — Base URL 末尾忘记加 `/v1`，或模型名不对（以控制台为准）。

**❌ `codex: command not found`** — npm 全局 bin 未加入 PATH：

```bash
echo 'export PATH="$(npm config get prefix)/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**❌ Node.js version too low** — Codex 要求 Node v22+，请升级：`nvm install 22 && nvm use 22`。

## 七、直接调用 API

端点同时支持 `/v1/chat/completions` 和 OpenAI 的 `/v1/responses`。发送 OpenAI 请求结构、得到 OpenAI 响应结构，现有工具链无需改动即可工作。
