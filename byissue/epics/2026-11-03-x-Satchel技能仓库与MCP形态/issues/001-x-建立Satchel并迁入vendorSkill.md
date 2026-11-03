---
kind: issue
title: "建立 Satchel 并迁入 vendor Skill"
type: feature
created: 2026-11-03
---

# 建立 Satchel 并迁入 vendor Skill

## 范围

在现有 `/Users/zijie/workspace/projects/satchel` 仓库中建立 Skill 区域，保留 Git 仓库与 satchel 二进制名。把旧包 `packages/pi-vendor/skills/pi-vendor` 和必要 shared JS 迁入新仓库，脚本只依赖 skill 内文件与 Node/Pi 安装。

## 验证

- 新仓库 Go 测试/构建与现有 Pi MCP 新路径正常。
- Skill 脚本语法/import/真实 catalog 查询通过。
- 旧 vendor 包卸载，新本地 Skill 注册且无同名重复。
- 不读取或输出 models.json 凭据；旧 monorepo 源码不修改。

## 执行记录

最终仓库保留在 `/Users/zijie/workspace/projects/satchel`，satchel 二进制与仓库内容完整。`skills/pi-vendor/` 已迁入完整 SKILL、evals、vendor.mjs、类型声明与 vendor-shared.js；import 改为 skill 内 `./vendor-shared.js`。

验证：

- `./scripts/test-skills.sh`：Node 语法、shared import/exports 全通过。
- 真实 `node .../vendor.mjs catalog kimi 3`：返回 official-catalog、3 条结果，无需旧包路径。
- 真 Pi 使用 pi-vendor Skill：实际读取新 SKILL.md 并运行新路径 vendor.mjs，返回 `kimi-for-coding`。
- 旧 `npm:@bytetrue/pi-vendor` 已卸载，新绝对路径已注册到全局 skills；无同名包重复。
- Pi MCP 命令已迁移到 Satchel 路径并 connected；旧 monorepo 源码未修改。
- 全量 Go test/vet/race 与六平台构建通过。

独立 review 功能项全部通过；其指出的 Project Spec vendor 旧归属和“Git 历史”不可核实表述均已修正。独立复核提出的问题均已修正，Issue 保持 open。

## 关闭结论

pi-vendor Skill 已自包含迁入 Satchel，脚本/catalog/真实 Pi 加载通过；旧 npm 包已卸载且本地 Skill 无重复。Skill 依赖与仓库边界已毕业到 Project Spec。 经用户授权交付关闭，无隐藏遗留事项。
