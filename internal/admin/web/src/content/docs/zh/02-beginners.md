---
slug: beginners
title: 新手必读
group: 开始使用
order: 2
intro: 第一次接触 API、命令行、环境变量？先花 3 分钟读完这一页，后面的客户端教程会顺畅 10 倍。
---

## 一、几个名词，一句话搞懂

| 名词 | 一句话解释 |
| --- | --- |
| **API** | 程序之间打交道的接口。你的客户端通过 API 跟 Claude / GPT 服务器对话。 |
| **API Key** | 一串字符，相当于身份证。客户端拿它去证明「我是付费用户」。本站格式 `sk-cpa-xxxx`，要保密、别提交到 Git。 |
| **Base URL** | 接口地址，告诉客户端「去哪里调」。本站是 `https://api.novadiffusion.com`。 |
| **环境变量** | 系统里的「全局配置」，CLI 工具启动时会读。本教程主要用到 `*_API_KEY` 和 `*_BASE_URL`。 |
| **命令行 / 终端 / CMD** | 三个叫法 = 同一个东西：能输文字命令的窗口。下面教你怎么打开。 |
| **npm** | 跟着 Node.js 一起装的「软件商店命令」。教程里很多命令以 `npm` 开头，作用是装 / 卸载工具。 |

## 二、怎么打开命令行（必备技能）

右上角的「系统」开关选你的系统，整页教程都会跟着切换。记住打开方式，后面所有命令都在这里敲。

<div data-tabs="open-terminal">
<div data-tab="macOS">

**打开「终端 Terminal」**

- 按 `Command (⌘) + 空格` 打开聚焦搜索，输入 `Terminal`（终端）回车。
- 或在「访达 → 应用程序 → 实用工具」里找到「终端」。

看到类似 `yourname@MacBook ~ %` 的提示符就对了，命令都在这里敲。

> **复制粘贴**：`⌘ + C` / `⌘ + V` 正常用即可。

</div>
<div data-tab="Windows">

**打开「PowerShell」**

- 按 `Win` 键，输入 `PowerShell`，点「Windows PowerShell」打开。
- 需要管理员权限时，右键选「以管理员身份运行」。

看到类似 `PS C:\Users\yourname>` 的提示符就对了。

> **复制粘贴**：复制用 `Ctrl + C`；粘贴在 PowerShell 里直接**右键**，或 `Ctrl + V`。

</div>
<div data-tab="Linux">

**打开「终端 Terminal」**

- 大多数发行版快捷键：`Ctrl + Alt + T`。
- 或在应用菜单里搜索 `Terminal` / `终端`。

看到类似 `user@host:~$` 的提示符就对了。

> **粘贴**：`Ctrl + Shift + V`（必须带 `Shift`，普通 `Ctrl + V` 在终端是别的功能）。

</div>
</div>

## 三、看到这些字眼别慌

**占位符 `sk-cpa-xxxxxxxx`** — 教程里所有出现的 `sk-cpa-xxxxxxxx` 都是假的占位符。你要把整段（包括 `sk-cpa-`）替换成控制台复制的真密钥，例如 `sk-cpa-abc123...`。注意不要保留 `xxxxxxxx`，要彻底换掉。

**符号 `~`（波浪号）** — macOS / Linux 终端里 `~` 代表你的家目录。`cd ~/Desktop` 等于进入桌面目录。

**命令前面的 `$` / `%` / `>`** — 这些是终端的提示符，不是命令的一部分。本站教程已经省略，你看到的就是要敲的。

**`command not found` / 不是内部或外部命令** — 对应程序没装、或装了但系统找不到它。每个客户端教程的「常见报错」段落都有解决方法，大多数「重启终端」就能修。

## 四、我该选哪个客户端？

| 你想要的 | 推荐 | 难度 |
| --- | --- | --- |
| 在编辑器里 AI 对话 + 补全（最适合新手） | **Cursor** | ⭐ 入门 |
| 用 Claude 在终端写代码 | **Claude Code** | ⭐⭐ 入门 |
| 用 GPT 在终端写代码 | **Codex CLI** | ⭐⭐ 入门 |
| 已有 Claude Code，想随时切服务商 | **CCSwitch** | ⭐⭐ 初级 |
| 完全不知道选啥 | 从 **Cursor** 开始 | ⭐ 入门 |

> Cursor 是图形界面，全程不用碰命令行，装好后在设置页填几个字段就行，最适合零基础。Claude Code / Codex 是命令行工具，按对应教程一步步来也很简单。
