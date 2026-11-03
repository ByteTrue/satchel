---
kind: epic
title: "补齐安装与发布分发"
created: 2026-11-03
---

# 补齐安装与发布分发

## 要改变什么

首版已有六平台归档、SHA-256、CI artifact 与 README，但缺少终端用户安装脚本、tag 后 release 资产发布流程和安装/升级/卸载文档。参考成熟 Go/CLI 产品，以 GitHub Releases 为版本真相，提供 macOS/Linux shell 与 Windows PowerShell 安装器，默认校验 SHA-256 并安装单二进制。

## 当前方案

- `scripts/install.sh`：Darwin/Linux × amd64/arm64，支持 `SATCHEL_VERSION` 与 `SATCHEL_INSTALL_DIR`，默认 `~/.local/bin`。
- `scripts/install.ps1`：Windows amd64/arm64，支持同名环境变量，默认 `$LOCALAPPDATA\Programs\Satchel`。
- 默认从 GitHub Releases 下载对应归档和 SHA256SUMS，校验通过后安装；不静默修改 shell profile 或系统 PATH，只打印明确后续命令。
- tag `v*` 触发 release workflow：复用测试和 `build-release.sh`，上传六资产与校验和。创建 workflow 不等于发布版本，实际 tag/release 仍需用户授权。
- README 增加安装、指定版本/目录、首次配置、MCP JSON、升级与卸载；仓库已按用户授权改为 public，raw/release 一键安装可匿名访问；尚无 release 时不得虚称 latest 已可下载。

不增加 Homebrew/Scoop 包、自动更新、签名/公证或包管理器发布，这些属于后续候选。关闭需用户明确授权。

## 推进视图

1. [001 提供安装脚本、release workflow 与用户文档](issues/001-x-提供安装脚本发布流程与用户文档.md)

## 关闭结论

Issue 001 已交付关闭，GitHub Releases 与安装器校验边界已毕业到 Project Spec，经用户授权关闭。
