---
kind: issue
title: "补齐四个搜索 provider"
type: feature
created: 2026-11-01
---

# 补齐四个搜索 provider

## 目标与范围

在 002 的五个搜索 API 上补齐 Epic 的九个 provider，并恢复无配置时 Exa-free 默认可用的体验。

本 issue 包含：

- `exa-free`：通过 Exa hosted MCP 的 `web_search_exa`；官方 Go MCP client/Streamable HTTP，session-per-search；SSE 多行 data 与 JSON 响应；限流提示。
- Exa Markdown 解析：优先带 Highlights 的 Title/URL 块，Markdown link 与 bare URL 回退；完整结果明确标为 unverified，不声称纯文本可以认证来源。
- `bing`：keyless HTML 搜索、b_algo 解析、Bing redirect URL 解码、最多两次尝试；200 大页面零结果视为挑战/布局变化并允许 fallback，而非成功空结果。
- `bocha`：`/v1/web-search`，summary/count/freshness，data.webPages.value。
- `jina`：`https://s.jina.ai/{escaped query}?num=N`，Authorization，data.results。
- 配置/setup 接受全部九个 provider；无显式链时默认 `exa-free`；keyless provider 不被强制要求 key。
- 002 的搜索回退、MCP 开关与 Pi 主链路不回归。

不包含：额外搜索服务、常驻 Exa session、模型原生搜索、浏览器 JS 渲染。

## 质量承诺

- Exa SSE 读取有响应预算，能处理 CRLF、多行 data、keep-alive 与最后一个可解析事件；不能因通知失败静默改变搜索结果。
- Bing 挑战页不能阻塞 fallback。Exa hosted MCP 只有不可认证 Markdown：保留多结果提取，但整个结果必须标为 unverified，并在文本和结构化 MCP 输出中提醒标题、URL、摘要可能由页面内容伪造；不承诺来源真实性。
- 默认无 key、无 Web 配置时 web_search 能走 exa-free；显式配置错误仍 fail closed，不回退成隐式默认。
- 默认测试无公网；只有实际 live 运行才声明 Exa/Bing 等可用。

## 风险与穿刺顺序

| 顺序 | 风险 | 怎样算打通 |
| --- | --- | --- |
| 1 | 官方 Go MCP client 与 Exa session/SSE 契约不一致 | fake Streamable HTTP 完成 initialize/initialized/tools/call，覆盖 session ID 与 SSE |
| 2 | 不可信 Markdown/HTML 产生伪结果或假空成功 | Bing 恶意 fixture 与 challenge 页面通过且 fallback 正确；Exa 完整伪块仍明确 unverified |
| 3 | 默认 exa-free 破坏显式配置 | 缺失配置默认成功；显式未知 provider/坏 JSON 仍报错 |
| 4 | 九 provider 配置和 keyless 校验不一致 | 九项 Load/Resolve/setup 表驱动测试通过 |

## 验证

- 全部 Go/race/vet 与真实 stdio；Pi 至少实测 exa-free fake MCP 主链路。
- 001–003 的既有集成测试不回归。

## 执行记录

已实现：

- `exa-free`：官方 Go MCP client + Streamable HTTP，固定 2025-03-26、session-per-search、`web_search_exa`；JSON/SSE、多行 data/CRLF/keepalive、session ID、4 MiB 总响应预算和 429 指引。
- Exa 不可信 Markdown：Highlights 块头部 Title/URL 优先，Markdown link/bare URL 回退；真实探针确认 structuredContent=nil，完整伪块与真实块不可区分，因此保留提取但统一标为 unverified。
- `bing`：DOM b_algo、Bing /ck/a base64 URL 解码、两次尝试；200 大页面零结果按挑战/布局失败并进入 fallback。
- `bocha`、`jina`：与现有 TS 的 payload、认证、响应路径对齐；九 provider 共用统一 Search/Hit/回退/脱敏。
- 配置与 setup 接受全部九项，exa-free/bing 不强制 key；缺失 provider 链默认 exa-free，显式空链/未知 provider 仍报错。Search(nil) 同样提供包级默认。
- 002 的成功 hits 四字段脱敏保持，001–004 的工具与协议不回归。

验证：

- `go test -count=1 ./...`、`go vet ./...`、`go test -race -count=1 ./...`：全部通过。
- Exa JSON/SSE/session/错误预算、恶意 Markdown（含完整伪块仍明确 unverified），Bing 挑战/重试/解码，Bocha/Jina golden 与既有五 provider 均有本地测试。
- 用户在真实协议探针后明确选择“保留多结果，明确标注未验证”，不以不可能的 Markdown 防伪承诺阻塞 keyless 能力。
- Firecrawl 保留 Magpie v2，404 时回退现有 TS 的 v1；双路径 golden test 通过。
- `TestSatchelStdioDefaultExaFreeSearch`：真实 Satchel stdio → fake Exa Streamable HTTP MCP，默认 exa-free 配置得到标题、URL、snippet，stderr 为空。
- 真实二进制 setup：全新配置连续回车接受默认 Exa-free，保存后 `config show` 解析确认 providers=`["exa-free"]`、baseUrl=`https://mcp.exa.ai/mcp`，无 key 提示或凭据。
- Pi 实测：独立 `pi -p` 调用 web_search 一次并返回 **Satchel Pi Exa Free Result**，确认没有走旧 Tavily 配置；未访问真实 Exa。
- 未对九个 provider 做真实公网 live；除 fake Exa 主链路外均只声明 fixture 覆盖。

独立 review 曾 BLOCK。修复结果：

- `Web.Providers` 移除 `omitempty`，empty/nil Save→Load 后仍失败关闭，不再变成默认 Exa-free。
- 显式 `web:null`（以及其他 object 区域 null）在 Load 拒绝。
- setup 不再给显式空链塞入 Tavily；空输入会报错，用户必须明确输入新链。
- MCP tool-level 429 复用 Exa 限流提示，新增 fixture。
- Exa 真实探针确认 structuredContent=nil；按用户选择保留多结果，并在文本显示 UNVERIFIED、structured output 返回 `unverified:true`，完整伪块不会误标可信。
- Firecrawl v2 404 回退 v1，认证与双路径均有测试。
- 修复后全量 `go test ./...`、vet、race 通过；九 provider Load/Resolve/setup 表驱动测试通过。最终独立复核 **OK / No issues found**，005 全部验收项 PASS。Issue 保持 open，关闭需用户明确授权。

## 关闭回写

关闭时把九 provider、keyless 默认和不可信结果解析约束回写 Epic；长期边界合回 Project Spec。

## 关闭结论

Exa-free/Bing/Bocha/Jina 已补齐，九 provider、keyless 默认、fail-closed 配置、stdio 和 Pi fake-provider 均通过。Exa 纯文本按用户确认保留多结果并明确 unverified；该边界已毕业到 Project Spec。 经用户授权交付关闭，无隐藏遗留事项。
