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
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"satchel/internal/config"
)

func TestSatchelStdioAnalyzeImageEndToEnd(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer stdio-vision-key" {
			t.Errorf("unexpected vision request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"stdio vision answer"}}]}`)
	}))
	defer provider.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	cfg := config.Default()
	cfg.Tools.GenerateImage = false
	cfg.Tools.WebSearch = false
	cfg.Tools.WebFetch = false
	cfg.Vision = config.VisionConfig{API: "openai-completions", BaseURL: provider.URL + "/v1", APIKey: "stdio-vision-key", Model: "vision-test"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(root, "tiny.png")
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\n"+"tiny"), 0o600); err != nil {
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
	client := mcp.NewClient(&mcp.Implementation{Name: "satchel-vision-e2e", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect stdio: %v; stderr=%s", err, stderr.String())
	}
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("tools/list = %+v, %v", tools, err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "analyze_image" {
			found = true
		}
		if tool.Name == "image_ask" {
			t.Fatal("legacy image_ask is still exposed")
		}
	}
	if !found {
		t.Fatal("analyze_image is not listed")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "analyze_image", Arguments: map[string]any{"image": imagePath, "prompt": "describe"}})
	if err != nil || result.IsError || mcpText(result) != "stdio vision answer" {
		t.Fatalf("analyze_image = %+v, %v", result, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if stderr.String() != "" {
		t.Fatalf("unexpected MCP stderr: %s", stderr.String())
	}
}
