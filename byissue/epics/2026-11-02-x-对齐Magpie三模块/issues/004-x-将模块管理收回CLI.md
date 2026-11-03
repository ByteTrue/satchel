---
kind: issue
title: "将模块管理收回 CLI"
type: fix
created: 2026-11-02
---

# 将模块管理收回 CLI

## 问题与修正

001 曾把三模块控制实现为 `satchel_list_modules` / `satchel_set_module` 两个 MCP 管理工具，这是控制面放错位置。用户明确要求：**用户通过 CLI 管理模块，Agent 不需要管理工具。**

## 目标与范围

- MCP 只暴露六个 Magpie 对齐业务工具，不再出现任何 Satchel 管理工具。
- CLI 提供 `satchel modules` 查看三模块状态，`satchel modules <vision|image|web-search> <on|off>` 修改状态。
- CLI 修改沿用原子配置写与坏配置保护；stdio MCP 在进程启动时按状态注册，CLI 修改后重启生效。
- 删除动态 Add/RemoveTools、list_changed 和 MCP SetModule 路径；setup 内的人工模块开关可以保留。
- 更新 README、AGENTS、Project Spec、Epic 与测试；001 的动态管理证据保留为历史，并由本 Issue 明确取代。

## 验证

- tools/list 只有按模块开放的六个业务工具，调用旧管理工具失败。
- 真实 CLI 修改配置；重启真实 stdio 后工具面变化。
- 真实 Pi 执行 `satchel modules vision off/on`，分别验证 analyze_image 消失/恢复。
- 全量 Go test/vet/race 与六平台构建不回归。

## 执行记录

已移除两个 MCP 管理工具、动态 SetModule/AddRemove/list_changed 路径；Server 只在启动时按模块注册六个业务工具。新增 `satchel modules` 与 `satchel modules <module> <on|off>`，沿用原子配置写，明确提示重启。

验证：

- SDK 负向测试确认 tools/list 无 `satchel_` 管理工具，旧管理调用失败；8 种模块组合只计算业务工具。
- 真实 stdio 测试通过 CLI 关闭/开放 Vision，新进程分别得到 0 个工具和仅 analyze_image。
- 真实 Pi：`satchel modules vision off` 后 5 个业务工具、无 analyze_image；`vision on` 后恢复 6 个；两次均无管理工具。
- 全量 Go test/vet/race、六平台构建与 SHA-256 校验通过。
- README、AGENTS、Vision、Project Spec、Epic 已改为纯 CLI 控制面；001 动态管理记录标记为历史。

独立复核曾指出 Epic 开头仍残留动态 MCP 管理描述，已改为纯 CLI 控制面并复查当前文档无旧描述。最终独立复核 **PASS / No issues found**。Issue 保持 open，关闭需用户明确授权。

## 关闭结论

Agent 管理工具已移除，MCP 仅暴露六个业务工具；satchel modules 原子持久化并在重启后生效。真实 Pi CLI off/on、全量测试和最终 review 通过，CLI-only 控制面已合回 Spec。 经用户授权交付关闭，无隐藏遗留事项。
