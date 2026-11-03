# AGENTS.md

Satchel is the user's MCP toolbox repository. The `satchel` binary is its independent Go single-binary MCP implementation. Pi-specific provider/model management belongs to pi-package-mono's small extensions, not this repository. The same `satchel` binary provides interactive configuration and a stdio MCP server. Do not add a Node/Python sidecar, daemon, model gateway, Pi dependency, or Magpie dependency.

## Commands

```bash
go test ./...
go build ./cmd/satchel
```

Default tests must not need live credentials or network access. Use `httptest` for provider contracts. Live Pi/provider checks are separate and must be reported as such.

## Boundaries

- Stdout in `mcp` mode is protocol-only; diagnostics go to stderr.
- The three modules are `vision`, `image`, and `web-search`. Users manage them only through `satchel modules` (or setup); MCP exposes no management tools. CLI changes persist atomically and apply after restarting the MCP process. `generation_result` belongs to `image`.
- Stdio exposes exactly one unified MCP through `satchel mcp`; module arguments and split mode are not supported. `satchel mcp-config` prints only one copyable `satchel` entry in `mcpServers` JSON with an absolute command path.
- Agent arguments never select providers/models or override configured credentials.
- Configuration writes are atomic and fail closed on malformed existing files; secrets are redacted.
- Configured default output directories are absolute. Magpie-compatible `path` arguments may be relative to the MCP process working directory or absolute; generated files are created exclusively and never overwritten.
- URL fetching preserves SSRF, redirect, timeout, response-size, and credential-leak protections.
- Keep runtime dependencies pure Go and minimal. Use the official `github.com/modelcontextprotocol/go-sdk/mcp` for MCP.

## Distribution

- GitHub Releases is the versioned distribution source. `scripts/install.sh` and `scripts/install.ps1` must verify `SHA256SUMS` before installing and must not silently edit profiles or PATH.
- CI exercises the installers on Ubuntu, macOS, and Windows using local release artifacts. `release.yml` publishes only after an explicitly authorized `v*` tag; never create a tag or release just to test an installer.

## Magpie reuse

Prefer selective copying of proven Magpie vendor protocol and safety code over clean-room reimplementation. Do not import or fork its gateway/provider registry. Replace wide gateway interfaces with Satchel's narrow provider/config values while preserving signing, timeouts, limits, and error handling.

Each copied source records its upstream path and the baseline commit. Keep `THIRD_PARTY_NOTICES.md` and included license texts accurate for the code and dependencies actually distributed.

## ByIssue

This repository uses ByIssue (`byissue/`). Read `byissue/spec/index.md`, the active Epic `spec.md`, and the specific Issue before implementation. Keep Vision (target), Project Spec (stable boundaries), Epic Spec (current bounded change), and Issue evidence separate.

Managed issues stay `-o-` during implementation. Record implementation and verification in the Issue. Do not rename/close Issues, close Epics, create releases, or push tags without explicit user authorization. Commit only when asked, and run the ByIssue open-issue check before a managed close/commit.
