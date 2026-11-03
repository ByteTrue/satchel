---
kind: epic
title: "Satchel 首个可分发版本"
created: 2026-11-01
---

# Satchel 首个可分发版本

## 要改变什么

从空仓库交付一个独立 Go 二进制，使用户能够完成配置、控制工具开放，并通过 stdio MCP 使用识图、生图、搜索和网页抓取。首版从 Vision 中摘取完整的四项能力与基本分发闭环，不摘取 daemon、HTTP、自动附件、视频或客户端自动安装。

产品名和二进制名均为 `satchel`。仓库位于 `/Users/zijie/workspace/projects/satchel`，不修改现有 Pi 扩展。

## 已确认范围

| 能力 | 首版范围 |
| --- | --- |
| `image_ask` | 1–4 张本地图片、总计 20 MiB；OpenAI Completions、OpenAI Responses、Anthropic Messages、Google Generative AI |
| `generate_image` | OpenAI、Gemini、DashScope、Ark、OpenRouter；文生图与参考图编辑；`n=1–8` |
| `generation_result` | 查询约 20 秒仍未完成的生图任务；任务仅在当前进程内有效 |
| `web_search` | Exa-free、Bing、SearXNG、Bocha、Tavily、Exa、Brave、Jina、Firecrawl，按配置回退 |
| `web_fetch` | 公开 HTTP(S) 页面抓取、charset、HTML 正文与链接、SSRF 和响应限制 |

协议范围对齐不代表逐项复制旧扩展的 TUI、模型别名或完整聊天能力。Vision 只有上述四种协议，不包含 Vertex、Bedrock、Codex 登录、Pi Messages 或任意 custom API。

## 用户界面与开放语义

同一个二进制提供：

```text
satchel setup
satchel config show
satchel mcp
satchel version
```

`setup` 使用简单编号菜单与逐项输入，密钥遮罩；不做全屏 TUI。无参数显示帮助。`mcp` 的 stdout 只传协议，日志写 stderr。

四个业务开关是 `image_ask`、`generate_image`、`web_search`、`web_fetch`，缺省开放。`generation_result` 跟随生图。MCP 启动时只注册已开放且已实现的工具，关闭项不可直接调用；开关在进程重启后生效。未配置 provider 与关闭工具是不同状态，前者在调用时给出配置提示。

配置位于操作系统用户配置目录，可用专用环境变量覆盖路径以支持测试。配置包含版本、四个开关、独立的 vision/image/web 区域。支持 literal secret、`$ENV_VAR` 和无 key endpoint；原子写、Unix `0600`、坏配置 fail closed、状态脱敏。生图输入与输出使用绝对路径，不依赖客户端 cwd 或 MCP Roots。

## 实现取舍：选择性复制，不继承网关

MCP 生命周期与 stdio 使用官方 `github.com/modelcontextprotocol/go-sdk/mcp`，保留 SDK 的协议版本协商，不手写 JSON-RPC。Exa-free 也可复用 SDK 的 client。

优先从 Magpie 基准 `4534561f6bb8f8353a67801fbe864a6502517022` 复制实际实现：

- 搜索 API：请求、响应解析、key 轮换/退避和错误脱敏。
- 网页抓取：正文转换、分页、公网地址与重定向防护。
- 生图：OpenAI Images、OpenRouter、chat image、Gemini、响应解析、多图循环与图片下载。
- Ark：必须保留 `draw_volcengine.go` 的专用请求、尺寸映射和逐张语义。
- 远程图片：同时保留 image fetch guard 与 netguard，不只复制下载函数。
- Vision：提取四种协议的图片 payload 与响应解析；不复制回绕 `s.serve` 的完整网关链路。

以窄的本地 provider 配置和 `net/http` client 替代 Magpie 的 `Provider.Sign`、registry、catalog、usage、账号、路由和插件。Bocha、Jina、Bing、Exa-free 等 Magpie 缺口从现有 TS 行为与测试补齐。复制文件记录来源，分发时附带实际需要的第三方 notice。

不选择完整 fork：provider/gateway 的包级闭包会带入数万行及账号、插件和全局副作用。也不从空白重写已有协议：那会浪费已有实现与测试。

## 质量与验证

- 每个协议/provider 使用 `httptest` 和 golden cases 验证 URL、payload、认证、图片顺序、响应与错误遮罩。
- 配置覆盖原子写、坏文件、env 展开、权限与零泄露。
- 网络覆盖 DNS、redirect、proxy、SSRF、body limit、charset、HTML 保真。
- 生图覆盖超过 20 秒转任务、查询、取消、进程退出边界、文件不覆盖。
- 工具开关同时验证 `tools/list` 与直接 `tools/call`；stdio 验证 stdout 纯净与取消传播。
- 默认测试不需要 live key。真实客户端只承诺 Pi；live provider 只声明实际运行的路线，其余以协议 fixture 验证。
- 发布前 `go test ./...`，并构建 macOS / Linux / Windows × amd64 / arm64，附 SHA-256。

## 推进视图

当前可推进：

- [001 打通生图复制与 stdio 主链路](issues/001-x-打通生图复制与stdio主链路.md)：以 OpenAI Images + Gemini native 证明复制裁剪、配置、开关、异步任务、文件和 Pi 接入整条链路。**主链路已打通，Go/race/vet 与 Pi fake-provider 实测通过，独立复核 PASS；URL 图片下载与远程参考图属于后续加厚。**

随后按风险加厚，而不是一次性重写全部协议：搜索与抓取复制、Vision 四协议、生图剩余协议、搜索缺口、完整 setup 与发布矩阵。每个后续切片创建时再形成独立 issue。

- [002 复制搜索 API 与安全网页抓取](issues/002-x-复制搜索API与安全网页抓取.md)：提取 Magpie 已有的 Tavily、Brave、Exa、Firecrawl、SearXNG 与 SSRF-safe fetch。**五 provider、独立开关、stdio、Pi fake-provider 与独立复核均 PASS；其余四个搜索 provider 属后续切片。**
- [003 实现四协议主动识图](issues/003-x-实现四协议主动识图.md)：OpenAI Completions/Responses、Anthropic、Google；**四协议 fixture、stdio、Pi fake-provider 与独立复核均 PASS。**
- [004 补齐生图协议与安全图片下载](issues/004-x-补齐生图协议与安全图片下载.md)：DashScope、Ark、OpenRouter 与远程图片；**五 API、协议/下载测试、OpenRouter stdio、Pi 回归与最终独立复核均 PASS。**
- [005 补齐四个搜索 provider](issues/005-x-补齐四个搜索provider.md)：Exa-free、Bing、Bocha、Jina 与 keyless 默认；**九 provider、全量 Go/race/vet、Exa stdio、Pi fake-provider 与最终独立复核均 PASS。**
- [006 完成分发矩阵与发布验收](issues/006-x-完成分发矩阵与发布验收.md)：六平台构建、依赖 notice、SHA-256、CI、全量回归与 Pi 五工具联合验收均已完成；首版关闭条件已满足，待用户授权收尾。

暂不推进：自动 hooks、HTTP/daemon、视频、vendor、自动迁移旧配置、自动更新、动态工具列表刷新和未经实测的全客户端兼容声明。

## 关闭条件与毕业候选

四项能力、全部已选协议与开关均可配置并通过相应验证；单二进制 setup/mcp 链路在 Pi 实测；六平台构建与发布说明齐全；notice 完整。关闭后，将稳定的配置语义、工具开放语义、复用边界与已证实网络约束合回 Project Spec，并把 Vision 的首版区域标为已实现。

## 关闭结论

六个 Issue 均已交付关闭。首版配置、四协议识图、五协议生图、九 provider 搜索、安全抓取、异步任务、CI 与六平台分发均有实现和验证；真实 Pi 已跑通五工具及异步生图。长期边界合回 Project Spec，维护陷阱沉淀到 notes/provider-and-file-safety.md。未实测的 live provider 不作兼容声明。 经用户授权关闭并毕业。
