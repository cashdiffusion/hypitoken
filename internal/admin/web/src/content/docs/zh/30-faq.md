---
slug: faq
title: 常见问题
group: 参考
order: 30
intro: 接入与计费的高频问题集合。
---

## 1. 配置后请求返回 401 Unauthorized？

检查：① 密钥是否完整复制（含 `sk-cpa-` 前缀）；② 配置是否已生效（重开终端 / 重启客户端）；③ 密钥是否被禁用或已删除。

## 2. 临时配置的环境变量在新终端失效？

用 `export`（Linux/macOS）、`$env:`（PowerShell）设置的环境变量仅在当前会话有效。请按各客户端教程的**永久配置**方式（`settings.json` / `config.toml`）设置。

## 3. Claude Code 和 Codex 的 Base URL 为什么不一样？

两者协议不同：

- Claude Code 用 Anthropic 协议，Base URL 是 `https://api.novadiffusion.com`（**不带** `/v1`）
- Codex / Cursor 用 OpenAI 协议，Base URL 是 `https://api.novadiffusion.com/v1`（**必须带** `/v1`）

## 4. 为什么不能用 curl / Python SDK / Postman 直接测接口？

本网关只接受**交互式 AI 编程客户端**（Claude Code、Codex CLI、Cursor、Claude Desktop）。原始 SDK、`curl`、`python-requests`、LiteLLM、Postman 等脚本类调用会在入口被拒绝，返回 `403 client not allowed`。请用上述官方客户端接入，不要用裸 `curl` 测试。

## 5. 余额扣费规则是什么？

按 Token 实时计费：每次请求结束后，按输入 Token 数 × 输入单价 + 输出 Token 数 × 输出单价从余额扣除。不同模型单价不同，以控制台 [价格](/docs/pricing) 页面为准。余额不足时 API 返回 `402 Payment Required`。

## 6. 是否支持流式响应？

支持。完全兼容 Anthropic / OpenAI 的流式 SSE 接口，无需额外配置，客户端默认就是流式输出。

## 7. 一个 Key 可以同时用在多个工具吗？

可以。同一个 Key 可同时用于 Claude Code、Codex CLI、Cursor，只需按工具协议填入对应 Base URL。若想分项目统计用量，也可为每个项目建独立 Key。（CCSwitch 不直接接入网关，它只是帮你管理 Claude Code / Codex 的服务商配置。）

## 8. 网关和官方有什么差异？

- **网络**：国内可直连，无需梯子，延迟更低
- **账号**：用网关签发的 Key 而非官方 Key
- **计费**：在网关充值，按用量扣费，通常比官方便宜
- **模型**：同时支持 Claude、GPT 等，不必分别开各家账号
- **限制**：模型版本以控制台为准

## 9. 命令行第一次见，完全不会用怎么办？

先看 [新手必读](/docs/beginners)，里面有怎么打开终端、怎么复制粘贴、什么是占位符的完整说明。也可以直接选 [Cursor](/docs/cursor)（图形界面），全程鼠标点击不用碰命令行。

## 10. 「复制密钥」时多复制了一个空格 / 换行怎么办？

这是 401 报错的常见原因。回 [密钥管理](https://api.novadiffusion.com/app/tokens)，点密钥旁的「复制」按钮（不要手动选文本），按钮复制保证不带多余字符，然后重新填到客户端。
