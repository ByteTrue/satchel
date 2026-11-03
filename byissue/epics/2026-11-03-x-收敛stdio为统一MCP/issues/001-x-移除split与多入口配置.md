---
kind: issue
title: "移除 split 与多入口配置"
type: fix
created: 2026-11-03
---

# 移除 split 与多入口配置

## 范围

删除 `satchel mcp <module>` 和 `satchel mcp-config --split`，收敛为统一 stdio MCP；保留 CLI 模块开关与单 server JSON 输出。更新 README、AGENTS、Project Spec 与测试，不改已关闭历史 Epic。

## 验证

- split 参数明确拒绝；mcp-config 只含 satchel 一个条目、绝对 command、可选 SATCHEL_CONFIG env。
- 真实 Pi unified 六工具正常，CLI 关闭/重开 Vision 后统一工具面变化。
- 全量 Go test/vet/race；不做视频 live。

## 执行记录

已删除 `satchel mcp <module>`、`mcp-config --split` 及 split 专用实现/测试。`satchel mcp` 只按 CLI 模块状态注册统一工具面；`satchel mcp-config` 不再需要读取配置来选择 server，只输出一个 satchel 条目与可选 env。

验证：

- 参数回归：`mcp vision` 与 `mcp-config --split` 均明确拒绝。
- mcp-config 严格 JSON 解析：只有 satchel，command 为当前二进制绝对路径，args 仅 `["mcp"]`。
- 真实 Pi：vision off 时只有一个 satchel、5 个工具；vision on 后恢复同一个 satchel、6 个工具。
- 全量 Go test/vet/race 通过；未做视频 live。
- README、AGENTS、Project Spec、Vision 已改为统一 stdio MCP，已关闭 Epic 的 split 决策仅保留为历史。

最终独立复核 **PASS / No issues found**。Issue 保持 open，关闭需用户明确授权。

## 关闭结论

已移除 split 模式与 --split 配置输出，收敛为单一 `satchel mcp` 与单条目 `satchel mcp-config`；真实 Pi、全量 Go 测试与独立复核 PASS。长期边界已合回 Project Spec，经用户授权关闭。
