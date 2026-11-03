package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"satchel/internal/config"
)

type fakeExaInput struct {
	Query      string `json:"query"`
	NumResults int    `json:"numResults"`
}

func TestSatchelStdioDefaultExaFreeSearch(t *testing.T) {
	upstream := mcp.NewServer(&mcp.Implementation{Name: "fake-exa", Version: "1"}, nil)
	mcp.AddTool(upstream, &mcp.Tool{Name: "web_search_exa", Description: "fake Exa search"}, func(_ context.Context, _ *mcp.CallToolRequest, input fakeExaInput) (*mcp.CallToolResult, any, error) {
		if input.Query != "codex cli" || input.NumResults != 6 {
			t.Errorf("Exa input = %+v", input)
		}
		text := "Title: CLI – Codex | OpenAI Developers\nURL: https://developers.openai.com/codex/cli\nHighlights:\nCodex CLI is OpenAI's coding agent."
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	})
	exa := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return upstream }, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	defer exa.Close()

	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	cfg := config.Default()
	cfg.Tools.ImageAsk = false
	cfg.Tools.GenerateImage = false
	cfg.Tools.WebFetch = true
	cfg.Web.BaseURLs["exa-free"] = exa.URL
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "satchel")
	build := exec.Command("go", "build", "-o", binary, "./cmd/satchel")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build satchel: %v\n%s", err, output)
	}
	command := exec.Command(binary, "mcp")
	command.Env = append(os.Environ(), "SATCHEL_CONFIG="+configPath)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "satchel-exa-e2e", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect stdio: %v; stderr=%s", err, stderr.String())
	}
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) != 2 {
		t.Fatalf("tools/list = %+v, %v", tools, err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "web_search" {
			found = true
		}
	}
	if !found {
		t.Fatal("web_search is not listed")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: map[string]any{"query": "codex cli"}})
	if err != nil || result.IsError {
		t.Fatalf("web_search = %+v, %v", result, err)
	}
	text := mcpText(result)
	for _, want := range []string{"CLI – Codex", "https://developers.openai.com/codex/cli", "OpenAI's coding agent", "UNVERIFIED"} {
		if !strings.Contains(text, want) {
			t.Errorf("result %q does not contain %q", text, want)
		}
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["unverified"] != true {
		t.Fatalf("Exa result was not marked unverified: %#v", result.StructuredContent)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if stderr.String() != "" {
		t.Fatalf("unexpected MCP stderr: %s", stderr.String())
	}
}
