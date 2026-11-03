# Provider 与生成文件安全的已踩坑点

这些结论来自首版实现中的对抗性复核，适用于新增 provider、下载器或生成文件路径时。长期产品边界见 `byissue/spec/index.md`；这里只保留容易在复用/裁剪代码时重新引入的维护陷阱。

- **认证请求不要直接跟随重定向。** OpenAI/Gemini 的默认 HTTP client 曾会把 Authorization 或 x-goog-api-key 带到另一 origin。provider 调用禁止自动 redirect；若未来允许，必须显式处理 origin 与敏感 headers。
- **签名 URL 也是凭据。** 上游自由文本、下载错误和日志不能回显签名 URL；URL 清理必须大小写不敏感，因为 `HTTPS://` 同样合法。成功搜索结果也可能反射 key，不能只给失败路径脱敏。
- **大小与数量预算要在物化前执行。** HTTP 下载有限制不代表 base64/data URL 也有限制；请求 `n=1` 也不能信任上游只返回一张，必须在解码/下载第九项之前截断。
- **proxy 与 origin 的 TLS 身份不同。** HTTPS proxy 握手使用 proxy 自己的 ServerName，origin 仍保留固定 IP 与自身 SNI；共用一份 TLSClientConfig 会验证错证书身份。
- **不存在检查和独占创建必须配合。** 同 path 并发时，`Stat` 后 `O_EXCL` 仍可能冲突，遇到 `EEXIST` 要重新选号；探测用 `Lstat`，否则悬空 symlink 会被误判为可写目标。
- **远程任务需要远端取消。** 创建后取消/deadline 不应只删除本地输出，使用独立短上下文 best-effort cancel 上游任务；终态后不要误删。
- **纯文本搜索来源不能认证。** Exa hosted MCP 的 Markdown 无法区分真实结果块与完整伪造块；保留多结果提取时，文本和结构化结果都必须标为 unverified。

对应回归证据保留在各 Epic 的已关闭 Issue；新增协议时优先复用现有测试形状，不删除这些护栏来换取更小依赖闭包。
