---
kind: epic
title: "对齐 Magpie 三模块"
created: 2026-11-02
---

# 对齐 Magpie 三模块

## 要改变什么

把 Satchel 从“名称相近的五个工具”改为“Magpie 三个内置 MCP 的独立合并版”：Vision、Image、Web Search 三个模块在同一个 Satchel stdio MCP 中暴露，保留各自 Magpie 工具名和输入契约，三个模块由用户通过 CLI 管理，MCP 不提供管理能力。

这不是引入 Magpie 网关。继续使用 Satchel 的窄配置、直接 provider 调用和现有安全边界；选择性复制 Magpie 的协议、schema 和文件语义。

## 工具面

| 模块 | Magpie 对齐工具 |
| --- | --- |
| `vision` | `analyze_image(image, prompt)` |
| `image` | `generate_image(...)`、`generate_video(...)`、`generation_result(id)` |
| `web-search` | `web_search(...)`、`web_fetch(...)` |

`generate_image` 对齐 Magpie stdio Image MCP 的 `prompt/size/n/reference_images/quality/background/path`；`generate_video` 对齐 `prompt/seconds/size/image/reference_images/path`。`path` 支持相对项目路径或绝对路径，文件永不覆盖；相对路径相对 MCP 进程工作目录解析。模块开放状态不等于 provider 已配置，未配置时工具给出 setup 指引。

模块管理只属于用户 CLI，Agent 没有管理工具：

```bash
satchel modules
satchel modules <vision|image|web-search> <on|off>
```

CLI 变更原子持久化，MCP 进程重启后按状态注册工具。Web Search 模块统一控制 search 和 fetch；Image 模块统一控制图片、视频和任务查询。配置可沿用现有四个工具布尔字段作为存储，外部语义只有三个模块。

## 视频边界

首版视频 provider 支持 OpenAI-shaped Videos API 与 Ark content-generation tasks；轮询、超时、512 MiB 下载和 MP4 不覆盖写入。账号订阅型 Grok/Codex、完整 Magpie provider registry 和路由组不复制。没有视频配置时 `generate_video` 仍按模块暴露，调用时返回可行动的配置错误。

## 质量与验收

- 工具名、必需参数、参数名和取值范围与 Magpie stdio 对齐；不再公开旧的 `image_ask`、`task_id`、`images`、`outputDir` 工具契约。
- 三模块四种以上开关组合验证 tools/list 与直接调用；CLI 修改原子持久化，重启真实 stdio 后生效。
- analyze_image 支持 Magpie 的绝对本地路径、HTTP(S) URL、data URL，保留大小/类型/SSRF/超时限制。
- 图片质量/背景、参考图、n=1–4、视频秒数/尺寸/首帧/参考图按 provider 能力传递或明确拒绝，不能静默改成其他模型。
- generation_result 统一处理图片和视频任务，使用 `id`。
- 全量 Go test/vet/race、真实 stdio、Pi 工具列表和 CLI 模块开关实测；真实 provider 只声明实际运行的路线。

## 推进视图

1. [001 对齐工具面与三模块动态开关](issues/001-x-对齐工具面与三模块动态开关.md)：**完成，最终独立复核 PASS。**
2. [002 对齐图片工具输入与文件语义](issues/002-x-对齐图片工具输入与文件语义.md)：**完成，最终独立复核 PASS。**
3. [003 实现视频生成与统一任务查询](issues/003-x-实现视频生成与统一任务查询.md)：**完成，最终独立复核 PASS。**
4. [004 将模块管理收回 CLI](issues/004-x-将模块管理收回CLI.md)：按用户修正移除 Agent 管理工具，改为纯 CLI 管理。

## 当前验收

- 真实 Pi 只暴露 6 个 Magpie 对齐业务工具；通过 CLI 关闭 Vision 后 analyze_image 不可见，重开恢复。
- 真实 analyze_image 与 generate_image（reference_images/path，经本地 Magpie → boldapi/gpt-image-2.5）成功。
- 真实 Pi 未配置视频时返回明确 setup 错误；视频能力由真实 stdio 慢任务 fixture 验证，未声称 live。
- 全量 Go test/vet/race、六平台构建及 SHA-256 校验通过。Issue 保持 open，等待用户明确授权关闭。

不推进：Magpie 账号体系、完整 gateway、HTTP/daemon、自动附件 hooks、provider 插件平台、vendor。关闭需用户明确授权。

## 关闭结论

四个 Issue 均已交付关闭。最终 MCP 只含六个 Magpie 对齐业务工具，模块由用户 CLI 管理；图片/视频/识图输入、文件语义、统一任务查询与远端取消均有测试和独立复核。真实 Magpie 生图与真实识图已通过，视频仅声明 fixture/stdio。控制面和安全边界合回 Project Spec。 经用户授权关闭并毕业。
