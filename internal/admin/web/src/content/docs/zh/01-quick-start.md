---
slug: quick-start
title: 快速开始
group: 入门
order: 1
intro: 一个统一网关同时接入 Claude 和 Codex。注册账号、创建令牌、完成首次调用，五分钟内即可开始。
---

## 一个地址，两个提供商

| 客户端 | Base URL |
| --- | --- |
| Claude Code | `https://api.novadiffusion.com` |
| Codex CLI | `https://api.novadiffusion.com/v1` |

同一个 API 令牌可同时用于 Claude Code 和 Codex CLI。Caddy 会把 `/v1/chat/*`、`/v1/responses*`、`/v1/models` 路由到 Codex 端点，其余流量进入 Claude 端点。

> 当前仅支持官方 Claude Code / Codex CLI 客户端。第三方 SDK 与工具
> （Anthropic SDK、OpenAI SDK、LiteLLM、自研聊天客户端等）会被网关层
> 的客户端指纹校验拦截。没有合法的 Claude Code / Codex CLI 签名时会返回
> `403 client not allowed`。

## 创建账号

在 `/register` 用邮箱 + 密码注册并完成邮箱验证。

## 准备客户端

| 系统 | Claude Code | Codex CLI |
| --- | --- | --- |
| macOS | 直接在 Terminal 安装并运行 | 直接在 Terminal 安装并运行 |
| Windows | 支持 PowerShell / CMD 原生运行，建议先安装 Git for Windows | 官方 CLI 当前建议在 Windows 11 + WSL2 中运行 |

Windows 用户不需要为了 Claude Code 专门安装 WSL。只有 Codex CLI 的官方 Windows 路径仍以 WSL2 为主；如果你只用 Claude Code，PowerShell 原生环境即可。

## 推荐配置方式

| 客户端 | 推荐永久配置 | 临时调试 |
| --- | --- | --- |
| Claude Code | `~/.claude/settings.json` 或 Windows `%USERPROFILE%\.claude\settings.json` | `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` 环境变量 |
| Codex CLI | `~/.codex/config.toml` + `~/.codex/auth.json`，Windows/WSL 对应用户目录相同 | `OPENAI_BASE_URL` / `OPENAI_API_KEY` 环境变量 |

后续文档会优先给出配置文件写法。环境变量适合一次性排查，不建议作为长期配置。

## 申请令牌

进入 **Tokens → New token**，可以设置：

- 单令牌月消费上限（USD，每自然月重置）

> 并发数和 RPM 使用服务器默认值，如需调整请联系管理员。

## 首次调用

```bash
curl https://api.novadiffusion.com/v1/messages \
  -H "Authorization: Bearer sk-cpa-•••" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 200,
    "messages": [{"role":"user","content":"你好"}]
  }'
```

> 请求会经过凭证池路由，响应保持上游原始格式返回。
