---
kind: epic
title: "Satchel 技能仓库与 MCP 形态"
created: 2026-11-03
---

# Satchel 技能仓库与 MCP 形态

## 要改变什么

把现有 Satchel 仓库作为用户的 Skill + MCP 伞仓库，保留 `satchel` 二进制与 MCP 产品名。迁入 pi-vendor 的 Skill 和运行脚本，不让 Skill 再依赖旧 npm 包目录。

MCP 增加两种用户可选暴露形态：

- `unified`：一个 `satchel` MCP，按模块开关暴露六个业务工具。
- `split`：三个独立 MCP——`satchel-vision`、`satchel-image`、`satchel-web-search`，分别运行 `satchel mcp <module>`。

新增 `satchel mcp-config [--unified|--split]`，stdout 只输出可直接复制的标准 `mcpServers` JSON，使用当前二进制绝对路径；`SATCHEL_CONFIG` 存在时带入 env。split 只输出当前开放模块。Agent 仍无模块管理工具。

## Vendor 边界

迁移 `skills/pi-vendor/` 完整 Skill、脚本和必要 shared JS；`vendor.mjs` 改为 skill 内相对 import，不再 import `../../../src`。不迁移 `/vendor` TUI、npm extension 或 Pi package 依赖。旧 monorepo 源码本轮不删除；本机在新 Skill 验证后卸载旧 vendor 包并以本地 Skill 路径替代，避免同名重复。

## 质量与验收

- 仓库移动后 Go test/vet/race 与六平台构建通过；现有 Pi MCP 命令更新到新路径。
- vendor.mjs 与 shared JS 语法/import 自检通过；Skill 可从新路径执行 catalog 查询；本机只发现一个 pi-vendor Skill。
- unified 工具面不回归；split 三进程分别只暴露 1/3/2 个业务工具，禁用模块不被输出。
- mcp-config 两种输出均为合法 JSON、命令为绝对路径、参数正确、密钥不出现；真实 Pi split 联调后恢复默认 unified。
- README/AGENTS/Specs 记录仓库、Skill、MCP 两种形态与复制配置命令。

## 推进视图

1. [001 建立 Satchel 并迁入 vendor Skill](issues/001-x-建立Satchel并迁入vendorSkill.md)
2. [002 支持 unified/split 与可复制 MCP JSON](issues/002-x-支持统一拆分MCP与配置输出.md)

不推进：vendor TUI 重写、MCP 管理工具、daemon/HTTP、删除旧 monorepo 源码、自动修改任意客户端配置。关闭需用户明确授权。

## 关闭结论

两个 Issue 均已交付关闭。Satchel 现为 Skill + MCP 伞仓库，pi-vendor Skill 自包含并在真 Pi 验证；MCP 支持 unified/split，mcp-config 输出可复制 JSON，真 Pi split 联调后恢复 unified。Skill 分发与 MCP 形态契约合回 Project Spec。 经用户授权关闭并毕业。
