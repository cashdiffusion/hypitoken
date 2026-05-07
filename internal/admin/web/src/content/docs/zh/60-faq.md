---
slug: faq
title: 常见问题
group: 参考
order: 60
---

## Advisor 模式能用吗?

可以。网关会为每个上游凭证维护完整的 Claude Code 会话身份，advisor、深度思考、子 agent、MCP 工具调用都按官方 CLI 行为转发。

## Windows 上必须用 WSL 吗?

不必须。Claude Code 官方支持 Windows 原生运行，可以在 PowerShell / CMD 中安装和使用。Codex CLI 当前官方 Windows 路径仍建议使用 Windows 11 + WSL2；如果你只使用 Claude Code，不需要为了它安装 WSL。

| 系统 | 建议 |
| --- | --- |
| macOS | Claude Code 与 Codex CLI 都可以直接在 Terminal 中安装 |
| Windows | Claude Code 用 PowerShell 原生运行；Codex CLI 优先用 WSL2 |

## 限流时会怎样?

代理层会自动在凭证池中跨凭证重试。如果触发的是你自己令牌的 RPM、并发或消费上限，响应会是 `429`，并带有 `Retry-After` 响应头。

## 我的数据存在哪里?

对话内容只在内存中穿过代理，不写入请求日志。令牌元信息、用量统计和计费账本会存入 SQLite。

## 可以用我自己的 API Key 吗?

可以。管理员可在 **Credentials** 标签页添加 API key 或 OAuth 凭证，池调度器会自动选择健康凭证处理请求。

## 什么是粘性会话?

每个客户端令牌在 10 分钟活跃窗口内的请求都会落到同一个上游凭证。这样可以跨多轮保持提示词缓存命中，并维持 advisor 的会话连续性。
