---
kind: epic
title: "收敛 stdio 为统一 MCP"
created: 2026-11-03
---

# 收敛 stdio 为统一 MCP

## 要改变什么

用户修正此前 unified/split 决策：HTTP 才需要考虑独立 endpoint 与聚合入口，Satchel 当前 stdio 形态只保留一个 MCP 进程和统一工具面。用户通过 CLI 的模块开关决定统一 MCP 中暴露哪些工具。

## 当前方案

- `satchel mcp` 是唯一 MCP 运行方式，不接受模块参数。
- `satchel mcp-config` 只输出一个 `satchel` stdio server 的标准 `mcpServers` JSON，不接受 `--split`。
- `satchel modules` 继续控制 vision/image/web-search；配置在 MCP 进程重启后生效。
- 移除 split 专用代码、测试和当前文档；已关闭 Epic 中的旧决策保留为历史。

不做视频 live 测试，不增加 HTTP/daemon，不恢复 Agent 管理工具。

## 推进视图

1. [001 移除 split 与多入口配置](issues/001-x-移除split与多入口配置.md)

## 验收

真实 CLI/stdio/Pi 只有一个统一 MCP；模块关闭后对应工具从统一 tools/list 消失，重启后恢复。mcp-config 为单 server 纯 JSON。全量 Go test/vet/race 不回归。关闭需用户明确授权。

## 关闭结论

Issue 001 已交付关闭，stdio 统一 MCP 与单条目配置输出边界已毕业到 Project Spec，经用户授权关闭。
