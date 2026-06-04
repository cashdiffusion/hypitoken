---
slug: ccswitch
title: CCSwitch 凭证管理
group: 辅助工具
order: 23
intro: CCSwitch 是一个本地凭证 / 配置管理工具，帮你管理 Claude Code、Codex 的服务商配置并一键切换，不用每次手动改环境变量。它本身不接入网关——真正发请求的仍是 Claude Code / Codex，所以前提是已装好对应的 CLI。
---

## 开始之前请确认

- 已注册 HypiToken 账号 + 创建密钥 + 充值（[快速上手](/docs/quick-start)）
- **本地已装好 Claude Code**，在终端能跑 `claude --version`（[如何安装](/docs/claude-code)）
- 能从 GitHub 或官网下载 CCSwitch 安装包

> **CCSwitch 不是独立客户端**：它的作用是帮你管理 Claude Code 的配置——一键切换不同服务商，不用每次手动改环境变量。所以前提是你必须已经装好 Claude Code。

## 一、关于 CCSwitch

CCSwitch 是一款在多个 Claude / AI 服务商之间快速切换的桌面工具。它会自动管理 Claude Code 所需的配置（`ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN`），让你无需每次手动改环境变量即可切换服务商。

| 项 | 值 |
| --- | --- |
| 协议 | Anthropic（透传给 Claude Code） |
| Base URL | `https://api.novadiffusion.com`（不带 `/v1`） |
| 前置 | 本地已安装 Claude Code |

## 二、下载安装

去 CCSwitch 的官网或 GitHub Releases 下载对应系统的安装包。

<div data-tabs="install-ccswitch">
<div data-tab="macOS">

下载 `.dmg` 拖进「应用程序」。首次启动需要授权访问 `~/.claude` 目录——若拒绝了，去「系统设置 → 隐私与安全性 → 文件夹」重新勾选。

</div>
<div data-tab="Windows">

下载 `.exe` 双击安装。启动后 CCSwitch 会自动检测本地已安装的 Claude Code。

</div>
<div data-tab="Linux">

下载 AppImage：

```bash
chmod +x ccswitch-*.AppImage
./ccswitch-*.AppImage
```

或下载 `.deb` / `.rpm` 包安装。

</div>
</div>

## 三、添加 HypiToken 服务商

1. 启动 CCSwitch，左侧菜单选 **Key 管理**，会看到已配置的服务商列表（默认有官方 Claude）。
2. 点 **添加** 按钮，填写：
   - **名称**：`HypiToken`
   - **Base URL**：`https://api.novadiffusion.com`
   - **API Key**：`sk-cpa-...`（你的网关 Key）
3. 保存后会出现在服务商列表中。

## 四、一键切换到 HypiToken

在 Key 管理页面，点 HypiToken 这一行右侧的「⋯」（三个点）按钮，选 **CC 切换**，Claude Code 就会切换为使用 HypiToken，无需重启 CCSwitch。

> **底层做了什么？** 「CC 切换」会自动改写 Claude Code 的 `~/.claude/settings.json`，设置 `ANTHROPIC_BASE_URL` 和 `ANTHROPIC_AUTH_TOKEN`，无需你手动配置。下次打开终端运行 `claude` 即走网关。

## 五、常见报错排查

**❌ 切换后 `claude` 仍走官方** — ① 没有重新打开终端，配置没生效；② 你的 shell 配置文件（`.zshrc` / `.bashrc`）里硬编码了 `ANTHROPIC_BASE_URL`，会覆盖 CCSwitch 的设置——去掉那行再试。

**❌ CCSwitch 找不到 Claude Code** — 需要先本地安装 Claude Code。先按 [Claude Code 教程](/docs/claude-code) 装好 `claude` 命令再回来切。

**❌ macOS 启动后授权失败** — 首次启动需授权访问 `~/.claude` 目录。去「系统设置 → 隐私与安全性 → 文件夹」重新勾选。
