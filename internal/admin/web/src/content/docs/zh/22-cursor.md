---
slug: cursor
title: Cursor 接入
group: 客户端接入
order: 22
intro: Cursor 是基于 VS Code 的 AI 代码编辑器。用 OpenAI 兼容模式（Override）填几个字段即可接入网关，全程无需命令行。
---

## 开始之前请确认

- 已注册 HypiToken 账号并创建密钥（[快速上手](/docs/quick-start)）
- 账户有余额或仍有新人试用额度（[如何充值](/docs/top-up)）
- 你的电脑能正常上网（Cursor 安装包从官网下，体积较大）

> **新手友好**：Cursor 是图形界面工具，全程不用打开终端，鼠标点击 + 填几个输入框就完事。被命令行劝退过的话，从 Cursor 开始最舒服。

## 一、端点信息

| 项 | 值 |
| --- | --- |
| 协议 | OpenAI Chat Completions API |
| OpenAI Base URL | `https://api.novadiffusion.com/v1` |
| API Key | 从控制台「密钥管理」复制，格式 `sk-cpa-...` |

> 本教程用 **OpenAI 兼容模式（Override）**：填 Base URL + Key 覆盖官方接口，网关上支持的 OpenAI 系模型都能用。Base URL 末尾**必须**带 `/v1`。

## 二、下载安装

去 [cursor.com](https://www.cursor.com) 下载对应系统的安装包。

<div data-tabs="install-cursor">
<div data-tab="macOS">

下载 `.dmg`，拖进「应用程序」即可。首次打开若提示「来自身份不明的开发者」，去「系统设置 → 隐私与安全性」点「仍要打开」。

</div>
<div data-tab="Windows">

下载 `.exe` 安装包，双击按引导安装。安装完成后从开始菜单启动 Cursor。

</div>
<div data-tab="Linux">

下载 AppImage：

```bash
chmod +x cursor-*.AppImage
./cursor-*.AppImage
```

若启动崩溃，尝试用沙箱禁用参数：`./cursor-*.AppImage --no-sandbox`。

</div>
</div>

## 三、在 Cursor 中配置网关

### 1. 打开设置页面

- 菜单：`File → Preferences → Cursor Settings`
- 快捷键：Windows / Linux `Ctrl + Shift + J`；macOS `Cmd + Shift + J`
- 或点右上角齿轮图标 ⚙️ → `Cursor Settings`

### 2. 填写 OpenAI API Key（Override）

在 Cursor Settings 左侧点 **Models**，滚到底部找到 **OpenAI API Key** 区域，填写：

| 字段 | 填写内容 |
| --- | --- |
| API Key | `sk-cpa-...`（你的网关 Key） |
| Base URL (Override) | `https://api.novadiffusion.com/v1` |

填完点 **Verify** 按钮，Cursor 会立即发一次测试请求验证连接。

```text
Cursor Settings
└── Models
    ├── 模型列表（勾选要启用的模型）
    └── OpenAI API Key（滚动到底部）
        ├── API Key:  [ sk-cpa-...                          ]
        ├── Base URL: [ https://api.novadiffusion.com/v1    ]
        └── [ Verify ] ← 点这里验证
```

> **点 Verify 后看到什么算成功？** 出现绿色对勾或 "Verified" 字样即成功。
> 失败排查：① Base URL 末尾有没有 `/v1`（最常见的错）；② Key 有没有多余空格 / 换行（回控制台重新点「复制」）；③ 余额是否充足；④ 换网络试试。

## 四、选择模型并验证

配置好 Key 后，在 **Models** 页面勾选要使用的模型。然后按 `Ctrl + L`（macOS：`Cmd + L`）打开对话窗口，在右上角下拉选刚启用的模型，输入测试：

```text
你好，帮我写一个 Hello World 的 Python 脚本
```

几秒内开始流式输出代码就说明配置成功。

> 具体可用的模型名以控制台 [价格](/docs/pricing) 页面为准。Cursor 的 Tab 补全功能依赖其专有补全模型，可能不走自定义 Base URL，以对话 / Chat 功能为主。

## 五、常见报错排查

**❌ 点 Verify 红色 / 验证失败** — ① Base URL 少了 `/v1`：正确是 `https://api.novadiffusion.com/v1`；② Key 复制不完整（检查 `sk-cpa-` 前缀）；③ 余额不足。

**❌ 对话没响应 / 一直转圈** — 网络问题稍等重试，或所选模型暂不支持，换一个。

**❌ Model not found** — 模型名有误，以控制台支持的模型列表为准，注意大小写与连字符。
