# Satchel

A portable MCP toolbox for AI agents.

The **satchel** binary is the repository's Go MCP component: the three Magpie capability MCPs—Vision, Image, and Web Search—extracted into one independent Go binary. It provides interactive configuration and a stdio MCP server without a daemon, external runtime, or Magpie provider registry.

> Status: Magpie-compatible tool names and inputs, three CLI-managed modules, image and video generation, four vision protocols, five image-generation protocols, nine web-search providers, and SSRF-safe web fetching. See the [alignment Epic](byissue/epics/2026-11-02-x-对齐Magpie三模块/spec.md) for verification status.

## Repository layout

- `cmd/`、`internal/` — Satchel CLI and MCP implementation.
- `byissue/` — product vision, specs, Epics, and implementation evidence.

Pi-specific `/vendor` provider/model management is maintained as a small extension in [pi-package-mono](https://github.com/ByteTrue/pi-package-mono/tree/main/small-extensions/pi-vendor), not in Satchel.

## Install Satchel

After a versioned GitHub Release is published, use the installer for your platform. It detects the OS/architecture, verifies the archive against `SHA256SUMS`, and installs the binary.

### macOS / Linux

```bash
curl -fsSL https://raw.githubusercontent.com/ByteTrue/satchel/main/scripts/install.sh | bash
```

Install a specific version or into a different directory:

```bash
curl -fsSL https://raw.githubusercontent.com/ByteTrue/satchel/main/scripts/install.sh | SATCHEL_VERSION=v0.1.0 bash
curl -fsSL https://raw.githubusercontent.com/ByteTrue/satchel/main/scripts/install.sh | SATCHEL_INSTALL_DIR="$HOME/.local/bin" bash
```

Default install location: `~/.local/bin/satchel`. If needed, add that directory to your shell's PATH; the installer prints the exact command.

### Windows

```powershell
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/ByteTrue/satchel/main/scripts/install.ps1 | iex"
```

For a specific version:

```powershell
$env:SATCHEL_VERSION = 'v0.1.0'
irm https://raw.githubusercontent.com/ByteTrue/satchel/main/scripts/install.ps1 | iex
```

Default install location: `$LOCALAPPDATA\Programs\Satchel\satchel.exe`. The installer prints a user-PATH command when needed; open a new terminal after changing PATH.

### First run, upgrade, uninstall

```bash
satchel setup
satchel mcp-config
```

Upgrade by rerunning the installer with the desired `SATCHEL_VERSION`. Uninstall by deleting `~/.local/bin/satchel` on macOS/Linux or `$LOCALAPPDATA\Programs\Satchel` on Windows. Configuration is kept separately under the OS user config directory; it is not deleted automatically.

The installer does not modify shell profiles or system PATH. Homebrew/Scoop packages and automatic updates are not currently included.

## Build from source

```bash
go build -o satchel ./cmd/satchel
```

## Configure

```bash
./satchel setup
./satchel config show
```

The default configuration is `os.UserConfigDir()/satchel/config.json`. Set `SATCHEL_CONFIG` to use a different file. `config show` redacts credentials.

Setup covers:

- OpenAI Completions/Responses, Anthropic Messages, or Google Generative AI for vision;
- OpenAI, Gemini, DashScope, Ark, or OpenRouter for images;
- OpenAI Videos or Ark for videos;
- an ordered Exa-free, Bing, SearXNG, Bocha, Tavily, Exa, Brave, Jina, and/or Firecrawl search chain;
- provider credentials/base URLs, absolute default output directories, and an optional web-fetch proxy.

The Agent calls tools with the configured provider/model; tool arguments cannot switch routes or credentials.

## Connect to Pi

```bash
pi mcp add satchel --exposure direct -- /absolute/path/to/satchel mcp
pi mcp list
```

If `SATCHEL_CONFIG` is needed, add it under the entry's `env` object. Avoid registering another extension that exposes the same direct tool names.

### One stdio MCP and copyable JSON

Satchel exposes exactly one unified stdio MCP. The CLI module switches below control which tools appear inside it; there are no split module commands.

```bash
satchel mcp
satchel mcp-config
```

`mcp-config` writes only a copyable `mcpServers` JSON document containing one `satchel` entry. It uses the binary's absolute path and includes `SATCHEL_CONFIG` under `env` when set.

## Modules and tools

| Module | Magpie-compatible tools |
| --- | --- |
| `vision` | `analyze_image(image, prompt)` |
| `image` | `generate_image(...)`、`generate_video(...)`、`generation_result(id)` |
| `web-search` | `web_search(query)`、`web_fetch(url, offset, max_chars)` |

Users manage modules through the CLI, never through Agent tools:

```bash
satchel modules
satchel modules vision off
satchel modules image on
satchel modules web-search off
```

Changes persist atomically and take effect after restarting the MCP process. MCP exposes only the six business tools above; it has no module-management tools.

`generation_result` belongs to the Image module and handles both image and video tasks. Tasks are in memory and do not survive process restart.

## Input and file semantics

- `analyze_image` accepts one absolute local image path, public HTTP(S) URL, or data URL. It does not intercept attachments or install host hooks.
- `generate_image` accepts `prompt`, `size`, `n=1–4`, `reference_images`, `quality`, `background`, and `path`.
- `generate_video` accepts `prompt`, `seconds=1–15`, `size`, `image`, `reference_images`, and `path`.
- Image/video `path` may be a file or folder, relative to the MCP process working directory or absolute. Names are derived from the prompt by default, numbered for multiple images and on conflicts, and existing files are never overwritten.
- OpenAI Images receives quality/background; other image protocols reject unsupported values explicitly.
- Video currently supports OpenAI-shaped Videos APIs and Ark content-generation tasks. Without video configuration, `generate_video` returns a setup error rather than silently using another model.

## Web behavior

- Exa-free is the keyless default. Its plain-text results are explicitly **unverified** because hosted Exa MCP provides no structured result metadata.
- Web search results are untrusted external content, not instructions.
- Firecrawl tries v2 and falls back to v1 on 404.
- `web_fetch` is not a JavaScript browser. It rejects private/loopback/link-local targets, rechecks DNS and redirects, and applies 30-second and 5 MiB budgets. An explicit proxy does not bypass target validation.

## Development

```bash
go test ./...
go vet ./...
go test -race ./...
```

Default tests use local fake providers and need no credentials. The stdio integration tests build and start the real binary through the official MCP client.

Build six platform archives with dependency notices and SHA-256 checksums:

```bash
./scripts/build-release.sh 0.1.0
```

Artifacts go to `dist/`; this does not tag or publish a release. After maintainer authorization, pushing a `v*` tag runs `release.yml`, reruns tests, and publishes the six archives and checksums to GitHub Releases.

## Acknowledgements

Satchel selectively adapts Magpie's provider protocols, MCP contracts, file targeting, and network-safety code from [Magpie](https://github.com/yetone/magpie), under the MIT License. See `THIRD_PARTY_NOTICES.md` for source and license information.
