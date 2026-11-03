---
kind: issue
title: "支持 unified/split 与可复制 MCP JSON"
type: feature
created: 2026-11-03
---

# 支持 unified/split 与可复制 MCP JSON

## 范围

- `satchel mcp` 保持 unified；`satchel mcp vision|image|web-search` 只暴露对应模块且尊重 CLI 开关。
- `satchel mcp-config [--unified|--split]` 输出标准 `mcpServers` JSON，无额外 stdout 文案；默认 unified。
- JSON 使用当前二进制绝对路径与正确 args；自定义 SATCHEL_CONFIG 通过 env 带入，不输出配置内容或密钥。
- split 仅包含当前开放模块，服务名为 satchel-vision/satchel-image/satchel-web-search。

## 验证

纯 JSON golden/解析测试、真实 stdio 三模块工具数、配置输出合法性与真 Pi split 联调；最后恢复 unified。全量 Go/race/vet 与六平台构建不回归。

## 执行记录

已实现 `satchel mcp [vision|image|web-search]` 和 `satchel mcp-config [--unified|--split]`。unified 保持六工具；split 三进程只暴露 1/3/2 业务工具并尊重模块状态。JSON 使用 os.Executable 绝对路径、正确 args 和可选 SATCHEL_CONFIG env，无额外 stdout。

验证：

- 纯 JSON 解析/golden、disabled module skip、真实 stdio split 工具面全通过。
- 真实 CLI unified/split 输出均为合法 JSON，命令为 Satchel 绝对路径。
- 真 Pi 临时接入 satchel-vision/image/web-search：三端 connected，共 6 个业务工具、无管理工具；随后恢复 unified，仍 6 个工具。
- 全量 Go test/vet/race、六平台构建与 SHA-256 校验通过。

独立 review 功能项全部通过；其指出的 AGENTS 未记录 unified/split/mcp-config 已补齐。最终独立复核 **PASS / No issues found**；Satchel 路径恢复后 mcp-config 输出也已复验。Issue 保持 open，关闭需用户明确授权。

## 关闭结论

unified/split 与 mcp-config 已实现；三进程 1/3/2 工具面、纯 JSON、禁用模块过滤和真实 Pi split 联调通过，最终恢复 unified。配置输出契约已毕业到 Project Spec。 经用户授权交付关闭，无隐藏遗留事项。
