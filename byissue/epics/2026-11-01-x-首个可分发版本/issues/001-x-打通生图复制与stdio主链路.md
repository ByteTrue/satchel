---
kind: issue
title: "打通生图复制与 stdio 主链路"
type: feature
created: 2026-11-01
---

# 打通生图复制与 stdio 主链路

## 目标与范围

交付首个可运行垂直切片：用户配置 OpenAI Images 或 Gemini native 生图路由，开放生图能力，通过 `satchel mcp` 在 stdio 上调用 `generate_image`；超过等待预算时获得任务 ID，并用 `generation_result` 取得不覆盖写入的本地图片。

本 issue 包含：

- 独立 Go module、`satchel` 命令入口和官方 Go MCP SDK。
- 配置文件的版本、生图区域、四个工具开关键；本切片只实现生图工具的注册与执行。
- 最小 `setup` / `config show`，支持 OpenAI 与 Gemini 生图配置、密钥和输出目录。
- 从 Magpie 选择性复制 OpenAI Images 与 Gemini 的下层请求/响应实现，以本地窄 provider 配置替代 gateway。
- 生图任务管理、绝对路径输出、文件名与自动序号、不覆盖写入。
- MCP 工具开关在 `tools/list` 和直接调用两侧生效。
- MIT / 来源记录和可运行测试。

不包含：其余生图协议、Vision、搜索、网页抓取、六平台 release、动态工具列表刷新、旧配置自动导入。未实现的开关不能宣称对应能力已可用。

## 必须修改 / 需要验证 / 仍未知

- 必须修改：新仓库全部初始代码与测试、README 的本切片使用说明、相关 notice。
- 需要验证：官方 SDK 与当前 Pi 的 stdio 互操作；复制后的 OpenAI/Gemini 请求与响应；长任务与取消；配置安全；现有 Pi 仓库保持不变。
- 仍未知：Magpie `drawImages/drawGemini` 对本地窄 provider 的最小适配面；Pi 对长工具调用与任务结果的实际展示。通过实现验证，不提前扩大抽象。

## 质量承诺

- 主链路端到端可观察：setup → MCP tools/list → generate → 本地图片。
- 凭据不进 stdout、日志、工具错误或文档；配置坏文件不被覆盖。
- Agent 不能通过参数切换 provider/model；输出路径必须为绝对路径，已有文件不能覆盖。
- 默认测试使用 fake provider，不需要真实 key；真实 Pi 验证单独记录。
- 复制保留签名、超时、图片读取、响应限制和错误处理，不以“穿刺”为由删安全护栏。

## 风险与穿刺顺序

| 顺序 | 风险 | 怎样算打通 |
| --- | --- | --- |
| 1 | Magpie draw 核心耦合 gateway，复制可能退化成完整 fork | OpenAI Images 与 Gemini 在本地窄配置下编译，并由 `httptest` 验证请求/响应；无 Magpie provider/gateway import |
| 2 | 官方 SDK、配置与 stdio 注册之间可能存在协议或生命周期误判 | 真实子进程完成 initialize/discover、tools/list、tools/call；stdout 无日志污染 |
| 3 | 长生成与 MCP 客户端超时不匹配 | fake provider 延迟超过首轮等待，返回 ID；generation_result 最终返回文件；取消路径有界 |
| 4 | 工具开关只隐藏列表却仍允许执行 | 关闭时列表无 generate/generation_result，直接调用失败；配置仍保留 |
| 5 | 路径与并发创建可能覆盖用户文件 | 并发/同名测试产生独立文件，路径逃逸与相对路径被拒绝 |

主路径未通之前不做其余 provider、额外 CLI 选项或发布配置。

## 验证

- `go test ./...`：配置、两个协议、任务、文件、开关和 stdio 子进程。
- 原生 `go build ./cmd/satchel`。
- 使用 fake provider 跑一次端到端演示。
- 配置可用后做一次 Pi 实测；若 live 凭据或上游不可用，明确保留为未验证，不用 fake 结果冒充。
- 确认 `pi-package-mono` 无改动。

## 执行记录

已实现：

- `cmd/satchel` 与 `internal/cli`：setup、config show、mcp、version；setup 当前只展示已实现的生图能力和开关。
- `internal/config`：版本化配置、默认工具开关、OpenAI/Gemini 路由、env 引用、脱敏、原子写与坏配置保护。集成修正了 Save 不得 chmod 已有父目录的边界。
- `internal/imagegen`：从 Magpie 选择性复制 OpenAI Images 与 Gemini native 协议；本地参考图、n=1–8、认证、响应限制、base64 图片和错误脱敏。没有 Magpie import 或 gateway/provider registry。
- `internal/mcpserver`：官方 Go SDK、工具注册与 schema、生图任务 ID、取消、结果保留、图片签名校验、绝对路径和不覆盖写入。
- README、THIRD_PARTY_NOTICES 与 Magpie 基准来源记录。

有界简化：本切片只接受本地绝对路径参考图，只处理 inline base64 生成结果；URL-only 结果明确报“尚不支持下载”，没有退化为不安全的裸 HTTP 抓取。远程参考图/结果下载连同 netguard 留到后续加厚。

穿刺结果：

| 风险 | 结果与证据 |
| --- | --- |
| Magpie 提取带入完整网关 | 通过；独立 `imagegen.Generate` 只依赖窄 Provider 与 net/http，OpenAI/Gemini golden tests 通过 |
| SDK/stdio 生命周期 | 通过；`TestSatchelStdioGenerateImageEndToEnd` 构建真实二进制，由官方 MCP client 完成 list/call |
| 长任务与取消 | 通过；manager 与 MCP round-trip 覆盖 pending→result、首次调用取消、等待取消、服务关闭 |
| 开关只藏列表不拦执行 | 通过；disabled MCP 测试确认列表不包含且直接调用不执行 GenerateFunc |
| 同名/并发覆盖文件 | 通过；独占创建测试保留两次不同内容，部分失败保留已成功文件 |

验证（2026-11-01）：

- `go test -count=1 ./...`：全部通过。
- `go vet ./...`：通过。
- `go test -race -count=1 ./...`：全部通过。
- `go build -o satchel ./cmd/satchel`：通过。
- 真实 stdio 集成：fake OpenAI 收到 `/v1/images/generations`、配置 model/prompt/n，生成文件字节与 provider 返回一致，stderr 为空。
- Pi 实测：隔离 `PI_CODING_AGENT_DIR` 下 `pi mcp list` 显示 satchel connected、`generate_image` 与 `generation_result` 两个 direct tools；独立 `pi -p` 实际调用 generate_image，fake provider 收到 prompt `a small red circle on a white background`，返回并生成 `/tmp/satchel-pi-smoke/images/image-20261101-130831.904090000-01.png`。未调用真实生图服务，未修改正式 Pi MCP 配置。
- Pi 仓库未由本实现修改；其已有 `.workbuddy/memory/2026-09-24.md` 删除状态保持不动。

独立 review 曾判定 BLOCK，发现两个凭据问题，均已修复并补回归：

1. Provider 默认跟随跨 origin redirect 会转发 Authorization/x-goog-api-key；现在浅拷贝 HTTP client 并禁止自动重定向，OpenAI/Gemini 双协议测试确认目标服务器不被调用、错误不泄密。
2. 手工 Image baseUrl 的 userinfo/query 可绕过脱敏；现在 Load 对非 env 的 baseUrl 做统一 URL 校验并 fail closed，Redacted 对异常 URL 再做纵深脱敏，Image/Web 均覆盖。

修复后 `go test -count=1 ./internal/imagegen ./internal/config ./internal/mcpserver ./internal/cli` 全部通过。独立复核最终 **OK / PASS，No issues found**，确认两个 blocker 均已关闭。完成实现、review 修正和验证不等于已关闭 issue；关闭仍需用户明确授权。

## 关闭回写

关闭时将稳定的开关语义、生图配置与复制约束回写 Epic；只有长期不变且代码无法表达的边界才合回 Project Spec。可复用的 Magpie 裁剪经验进入 `byissue/notes/` 候选。

## 关闭结论

OpenAI/Gemini 生图穿刺、配置、stdio、异步任务和文件安全均已实现；Go/race/vet、真实 stdio、Pi fake-provider 与最终独立 review 通过。长期边界已合回 Project Spec；URL 下载等加厚已由后续 Issue 接管。 经用户授权交付关闭，无隐藏遗留事项。
