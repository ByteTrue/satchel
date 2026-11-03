package cli

import (
	"bytes"
	"context"
	"fmt"
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

func TestSatchelStdioWebToolsEndToEnd(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.Header.Get("Authorization") != "Bearer web-secret" {
			t.Errorf("unexpected search request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"title":"Stdio search","url":"https://example.test/result","content":"search snippet"}]}`)
	}))
	defer search.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Stdio fetch</title></head><body><a href="/next">Next</a><p>fetch body</p></body></html>`)
	}))
	defer proxy.Close()

	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	cfg := config.Default()
	cfg.Tools.ImageAsk = false
	cfg.Tools.GenerateImage = false
	cfg.Web.Providers = []string{"tavily"}
	cfg.Web.APIKeys["tavily"] = "web-secret"
	cfg.Web.BaseURLs["tavily"] = search.URL
	cfg.Web.Proxy = proxy.URL
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
	client := mcp.NewClient(&mcp.Implementation{Name: "satchel-web-e2e", Version: "0"}, nil)
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
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "web_search", "web_fetch":
		default:
			t.Errorf("unexpected tool %q", tool.Name)
		}
	}

	searchResult, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: map[string]any{"query": "stdio"}})
	if err != nil || searchResult.IsError || !strings.Contains(mcpText(searchResult), "Stdio search") {
		t.Fatalf("web_search = %+v, %v", searchResult, err)
	}
	fetchResult, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_fetch", Arguments: map[string]any{"url": "http://93.184.216.34/start", "max_chars": 1000}})
	if err != nil || fetchResult.IsError {
		t.Fatalf("web_fetch = %+v, %v", fetchResult, err)
	}
	text := mcpText(fetchResult)
	for _, want := range []string{"Stdio fetch", "fetch body", "http://93.184.216.34/next"} {
		if !strings.Contains(text, want) {
			t.Errorf("web_fetch text %q does not contain %q", text, want)
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if stderr.String() != "" {
		t.Fatalf("unexpected MCP stderr: %s", stderr.String())
	}
}

func mcpText(result *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
