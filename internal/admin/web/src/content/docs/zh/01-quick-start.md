---
slug: quick-start
title: 快速开始
group: 入门
order: 1
intro: 一个统一网关同时承载 Claude 和 Codex。注册账号 → 申请令牌 → 首次调用,五分钟内搞定。
---

## 一个地址,两个提供商

| 客户端 | Base URL |
| --- | --- |
| Claude Code | `https://api.novadiffusion.com` |
| Codex CLI | `https://api.novadiffusion.com/v1` |

同一个 API 令牌对两边都生效。Caddy 把 `/v1/chat/*`、`/v1/responses*`、`/v1/models` 路由到 Codex 端点,其余流量都进 Claude 端点。

> 仅支持官方 Claude Code / Codex CLI 客户端。第三方 SDK 与工具
> (Anthropic SDK、OpenAI SDK、LiteLLM、自研聊天客户端等) 会被网关层
> 的客户端指纹校验拦截 —— 没有合法的 CC / Codex CLI 签名就会
> `403 client not allowed`。

## 创建账号

在 `/register` 用邮箱 + 密码注册并完成邮箱验证。

## 申请令牌

进入 **Tokens → New token**,可以设置:

- 单令牌总消费上限 (USD,每自然月重置)

> 并发数和 RPM 走服务器默认值,如需定制限额请联系管理员。

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

> **就这样**。请求经过凭证池路由后,响应原样返回。
