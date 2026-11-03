# Satchel Project Spec

Satchel 是用户的 Skill + MCP 伞仓库；`skills/` 提供可移植 Agent Skills，其中的 satchel 二进制是独立的 Go 单二进制 MCP 产品：同一个命令提供用户配置和 stdio MCP Server，让 Agent 使用用户明确开放的外部能力。首版已交付；已实现行为以代码和测试为准，目标范围见 Epic 与 Vision。

## 长期边界

### 独立而轻量

- 仓库和发布独立于 `pi-package-mono`。satchel MCP 二进制不能要求 Pi 扩展、Node、Python、Magpie 进程或网络网关作为运行前提；`pi-vendor` 等 Skill 可显式依赖 Node/Pi。
- 每个发布目标是独立可执行文件；使用纯 Go 依赖并支持交叉编译。
- 首版只提供 stdio。常驻 daemon、监听端口、远程认证与远程文件语义不是隐含功能。

### 用户控制能力与成本

- 三个用户可见模块是 `vision`、`image`、`web-search`；关闭后其工具既不能出现在 MCP 工具列表，也不能被直接调用。
- `generation_result` 属于 Image 模块，统一查询图片和视频任务；Web Search 统一控制 search/fetch。
- 模块管理只通过用户 CLI：`satchel modules` 查看，`satchel modules <module> <on|off>` 修改。MCP 不提供任何管理工具；CLI 变更原子持久化并在 MCP 进程重启后生效。
- 模型和 provider 只能由用户配置决定，Agent 工具参数不能自行切换路由或放大成本。
- 关闭能力不删除其凭据和模型配置。

### MCP 形态与 Skill 分发

- `satchel mcp` 是默认 unified 形态；`satchel mcp <vision|image|web-search>` 是 split 形态。两种形态共用配置与模块开关，不复制业务逻辑。
- `satchel mcp-config [--unified|--split]` 只输出标准 `mcpServers` JSON，使用二进制绝对路径；`SATCHEL_CONFIG` 仅作为 env 路径透传，split 跳过关闭模块，不输出配置内容或密钥。
- `skills/pi-vendor/` 必须自包含，脚本只引用 Skill 内部文件及明确声明的 Node/Pi 依赖；旧 npm 包目录与 `/vendor` TUI 不是 Skill 的运行前提。

### 不扩张成平台

- 不做完整聊天模型网关、账号订阅体系、插件平台、路由组或自动附件改写。
- 不复制 Magpie 的 UI、完整 provider 全局 registry、配额和账号体系来换取表面上的复用。
- Pi 只是首个实测客户端；Satchel 不承接 Pi hooks 或 Pi 配置管理，独立的 `pi-vendor` Skill 由本仓库 `skills/pi-vendor/` 提供。

### 复用必须有来源与边界

- 优先选择性复制 Magpie 的 vendor 协议、安全下载和文本转换实现，以窄的本地配置和 HTTP 边界替代其 gateway。
- 复制代码遵循 MIT：保留 yetone 版权与许可文本，记录上游路径和基准提交；第三方 notice 按实际保留代码与依赖生成。
- 不为复用而引入完整 Magpie 依赖闭包，也不为裁剪而删除签名、超时、SSRF、响应限制或错误脱敏。

## 不可退让的质量约束

- stdio 的 stdout 只承载 MCP 消息，诊断只写 stderr。
- 配置写盘须原子化；Unix 私钥文件权限为 `0600`；损坏配置拒绝静默覆盖；状态与错误输出必须脱敏。
- 生图、视频与下载文件必须独占创建，不能覆盖用户已有文件；Magpie-compatible `path` 可相对 MCP 进程 cwd，配置默认输出目录必须绝对。
- 远程视频任务创建后，调用取消或超时应以独立短超时 best-effort 取消上游任务，不能只丢弃本地等待而让付费任务失控。
- 本地图片和远程响应必须有明确数量、大小、类型和超时预算。
- 任意 URL 抓取须防 SSRF，包含 DNS、重定向与 proxy 路径；HTML 正文提取需保持链接与基本代码/表格结构。
- 搜索与网页结果均为不可信外部内容。Exa-free 的纯文本结果无法认证来源，必须在文本和结构化输出中明确标为 unverified，不能暗示真实性保证。
- 默认测试不依赖真实凭据；只有实际跑过的协议、provider、平台和客户端才能声明已验证。

## 阅读路径

- 产品目标与后续可能：`byissue/vision/`。
- 首版目标方案：`byissue/epics/2026-11-01-x-首个可分发版本/spec.md`。
- 当前 Magpie 三模块对齐与 CLI 管理：`byissue/epics/2026-11-02-x-对齐Magpie三模块/spec.md`。
- Satchel 技能仓库、vendor Skill 与 unified/split MCP：`byissue/epics/2026-11-03-x-Satchel技能仓库与MCP形态/spec.md`。
- 具体实现与验证证据：对应 Epic 下的 `issues/`。
