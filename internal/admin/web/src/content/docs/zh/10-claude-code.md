---
slug: claude-code
title: Claude Code 接入
group: 客户端
order: 10
intro: 30 秒把官方 Claude Code CLI 指向网关。Advisor、深度思考、子 agent 全部原生可用。
---

## 安装 Claude Code

```bash
# macOS / Linux
npm install -g @anthropic-ai/claude-code

# 验证
claude --version
```

> **Windows.** 先装 [WSL2](https://learn.microsoft.com/windows/wsl/install),然后在 Ubuntu 里跑上面的 npm 命令。

## 指向网关

设置两个环境变量,加到 `~/.zshrc` / `~/.bashrc` 持久化:

```bash
export ANTHROPIC_BASE_URL="https://api.novadiffusion.com"
export ANTHROPIC_AUTH_TOKEN="sk-cpa-••••••••••••••••••••••••••••••••"
```

> **`ANTHROPIC_AUTH_TOKEN`** 是官方 Claude Code OAuth 流程内部使用的变量,设置后 CC 会发 `Authorization: Bearer <token>` —— 正好是网关期望的格式。

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

所有 Claude Code 特性走网关都开箱即用,无需额外配置:

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

网关为每个客户端令牌维护**粘性会话**:你的 CC 会话在 10 分钟活跃窗口内始终落到同一个上游凭证,保证多轮缓存命中和会话连续性。

如果该凭证不可用,池管理器会自动切到下一个最健康的凭证 —— 不会丢请求。
