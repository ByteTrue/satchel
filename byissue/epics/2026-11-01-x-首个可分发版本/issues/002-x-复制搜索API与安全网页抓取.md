---
kind: issue
title: "复制搜索 API 与安全网页抓取"
type: feature
created: 2026-11-01
---

# 复制搜索 API 与安全网页抓取

## 目标与范围

把 Magpie 的独立搜索 API 与网页抓取核心接入同一个 Satchel stdio MCP 二进制：用户可分别开放 `web_search` 和 `web_fetch`，按配置顺序回退搜索 provider，并安全读取公开网页。

本 issue 包含：

- `web_search`：Tavily、Brave、Exa、Firecrawl、SearXNG；统一标题、URL、摘要；错误回退与多 key 拒绝后退避。
- `web_fetch`：公开 HTTP(S)、SSRF/DNS/redirect/proxy 防护、30 秒与 5 MiB 预算、charset、HTML 正文/链接、offset/max_chars 分页。
- 配置增加 `web.providers`、`web.apiKeys`、`web.baseUrls`、`web.proxy`，支持 `$ENV_VAR` 与状态脱敏。
- setup 增加本切片已实现的搜索 provider 与两个 Web 工具开关。
- MCP 注册、直接调用拦截、文本与结构化结果。
- 选择性复制 Magpie `search_api.go`、`search_fetch.go`、netguard 的实质实现与来源 notice。

不包含：Exa-free、Bing、Bocha、Jina；模型原生搜索；JS 浏览器渲染；远程 HTTP MCP；You.com/DeepSeek 等 Epic 未选择的额外 provider。搜索无配置时给出 setup 指引，Exa-free 的零配置默认在对应后续 issue 再引入。

## 必须修改 / 需要验证 / 仍未知

- 必须修改：web 配置、独立搜索与抓取包、MCP/CLI 适配、setup、README、测试和 issue 证据。
- 需要验证：001 的生图与 Pi 主链路不回归；两个 Web 开关互不影响；generic fetch 不随 search provider 改变；所有 live 声明只限实际运行的 fake/public 路径。
- 仍未知：Magpie publicClient/netguard 的最小提取面与 proxy 语义；通过复制穿刺确定，不能为通过测试而默认放行私网。

## 质量承诺

- 任意 Agent URL 默认拒绝 loopback、private、link-local 等非公网目标，每次 redirect 重新校验；proxy 不得绕过策略。
- 搜索 key 不进入 URL 日志、错误或 MCP 输出；跨 origin redirect 不转发 key。
- 读取有墙钟和字节预算，HTML 保留链接及基本代码/表格结构；offset 使用字符而非字节。
- 搜索按用户顺序回退，空结果与失败可诊断；多 key 只在认证/额度/限流类失败时轮换和退避。
- 默认测试无需真实 key，不访问公网；生产默认不接受用于测试的私网放行开关。

## 风险与穿刺顺序

| 顺序 | 风险 | 怎样算打通 |
| --- | --- | --- |
| 1 | netguard/publicClient 提取时丢失 DNS/redirect 防护 | fake DNS/transport 覆盖初始私网、redirect 到私网、proxy 与预算；生产默认无 private bypass |
| 2 | 搜索复制被 Magpie Server/provider 宽接口绑住 | 五个 provider 在本地窄配置下通过 `httptest` golden tests，无 Magpie import |
| 3 | 两个工具开关互相耦合 | 四种开关组合验证 tools/list 与直接调用，generic fetch 独立可用 |
| 4 | HTML/charset 简化造成内容失真 | 现有 TS 与 Magpie fidelity cases 对照，链接、代码、表格、GBK、offset 有回归测试 |

## 验证

- `go test -count=1 ./...`、`go vet ./...`、`go test -race -count=1 ./...`。
- 真实 stdio client 分别调用 web_search 与 web_fetch，并验证关闭项不可执行。
- Pi 实测 fake search/fetch 主链路；001 生图 smoke 不回归。
- 不运行需要真实搜索 key 的 live e2e；未实测 provider 明确标注 fixture-only。

## 执行记录

已实现：

- `internal/websearch`：选择性复制 Magpie 独立搜索 API；Tavily、Brave、Exa、Firecrawl、SearXNG 的请求/响应、顺序回退、多 key 轮换/退避、same-origin redirect、错误脱敏和统一 hits。
- `internal/netguard` + `internal/webfetch`：公网 IP 分类、所有 DNS 答案校验、地址固定、逐 redirect 校验、显式 proxy 下仍验证目标；30 秒、5 MiB、文本类型、charset、HTML 正文/链接与 rune 分页。
- `internal/config` / `internal/cli`：Web providers/apiKeys/baseUrls/proxy、env 解析、脱敏、setup 与两个独立开关；web_fetch 不依赖搜索链。
- `internal/mcpserver`：web_search/web_fetch schema、输入校验、结构化与文本结果；四种开关组合均验证 list/call。
- README 与 Magpie 来源 notice；没有 Magpie import。

穿刺结果：

| 风险 | 结果与证据 |
| --- | --- |
| DNS/redirect/proxy SSRF | 通过；netguard 地址形态、私网、redirect、固定拨号和 proxy 测试通过；CLI 以公网 IP + 本地 fake proxy 验证无公网主链路 |
| Magpie 宽接口耦合 | 通过；五个 provider 在窄 Provider 下 `httptest` golden tests 通过 |
| Web 开关耦合 | 通过；both off/search only/fetch only/both on 均验证列表与直接调用 |
| HTML/charset 保真 | 通过；链接、代码/表格、GBK、分页与既有 fidelity cases 通过 |

验证：

- `go test -count=1 ./...`、`go vet ./...`、`go test -race -count=1 ./...`：全部通过，001 包未回归。
- `TestSatchelStdioWebToolsEndToEnd`：真实二进制 + 官方 MCP client 分别调用 web_search/web_fetch；fake Tavily 与显式 fake proxy 成功，stderr 为空。
- Pi 实测：`pi mcp list` 显示四个 direct tools；独立 `pi -p` 各调用一次 web_search 和 web_fetch，返回 **Satchel Pi Search Result** 与 **Satchel Pi Fetch Result**。fake 日志分别只有 `/search` 与 `http://93.184.216.34:80/start` 各一次。未使用真实搜索 key 或公网。
- 五个 provider 中，除当前 fake Tavily 主链路外均只声明协议 fixture 验证，未声称 live 成功。

保持边界：无 Exa-free/Bing/Bocha/Jina、无模型原生搜索、无 JS 渲染、无生产私网 bypass。

独立 review 判定 BLOCK 并发现四项：IPv6 `2001:2::/48` 漏判、setup 回显 proxy 密码、成功 hits 未脱敏、HTTPS proxy 错用 origin SNI。四项均已修复并补回归：netguard 拒绝 2001:2::/48；setup 空输入保留 proxy 但不回显；成功 hits 四字段统一脱敏；HTTPS proxy 用独立 tls.Dialer 和 proxy 身份，origin SNI/固定 IP 不变。`go test -count=1 ./internal/netguard ./internal/webfetch ./internal/websearch` 通过。独立复核最终 **PASS / No issues found**，确认四项全部关闭；相关 CLI 测试也通过。Issue 仍保持 open，关闭需用户明确授权。完成实现和验证不等于已关闭 issue。

## 关闭回写

关闭时把 provider 支持现状、generic fetch 独立性和已证实安全约束回写 Epic；长期不变的网络与凭据边界合回 Project Spec。可复用的 Magpie 提取经验进入 notes 候选。

## 关闭结论

五个搜索 API 与安全 web_fetch 已交付，独立开关、stdio 和 Pi 实测通过。IPv6 特殊网段、proxy SNI、proxy 密码及成功结果脱敏问题均已修复并复核。网络安全经验沉淀到 notes/provider-and-file-safety.md。 经用户授权交付关闭，无隐藏遗留事项。
