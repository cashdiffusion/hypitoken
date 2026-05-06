---
slug: codex-cli
title: Codex CLI 接入
group: 客户端
order: 20
intro: OpenAI 官方 Codex CLI 通过 `/v1` 接入统一网关。
---

## 安装 Codex CLI

```bash
# macOS / Linux
npm install -g @openai/codex

# 验证
codex --version
```

## 指向网关

```bash
export OPENAI_BASE_URL="https://api.novadiffusion.com/v1"
export OPENAI_API_KEY="sk-cpa-••••••••••••••••••••••••••••••••"
```

> **同一个令牌,同一个域名。** Claude Code 和 Codex 共用网关域名和 API 令牌。Caddy 把 `/v1/chat/*`、`/v1/responses*`、`/v1/models` 路由到 OpenAI 兼容端点,其余 (`/v1/messages`) 进 Claude。

## 运行

```bash
# 交互式 Codex 会话
codex

# 一次性
codex "解释这条堆栈"
```

## 直接调 API

端点同时支持 `/v1/chat/completions` 和 OpenAI 新的 `/v1/responses`。POST OpenAI 请求格式、收到 OpenAI 响应格式,现有工具链零改动可用。
