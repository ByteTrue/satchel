package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

func TestSatchelStdioGenerateImageEndToEnd(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + "fake-provider-image")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("provider path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "gpt-image-test" || body["prompt"] != "a stdio cat" || body["n"] != float64(1) {
			t.Errorf("provider payload = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"b64_json":%q,"revised_prompt":"a clearer cat"}]}`, base64.StdEncoding.EncodeToString(png))
	}))
	defer provider.Close()

	root := t.TempDir()
	outputDir := filepath.Join(root, "images")
	configPath := filepath.Join(root, "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	cfg := config.Default()
	cfg.Tools.ImageAsk = false
	cfg.Tools.WebSearch = false
	cfg.Tools.WebFetch = false
	cfg.Image.Default = config.ImageDefault{Provider: "fake", Model: "gpt-image-test"}
	cfg.Image.OutputDir = outputDir
	cfg.Image.Providers["fake"] = config.ImageProvider{API: "openai", BaseURL: provider.URL + "/v1", APIKey: "test-secret"}
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
	client := mcp.NewClient(&mcp.Implementation{Name: "satchel-e2e", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect stdio: %v; stderr=%s", err, stderr.String())
	}
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) != 3 {
		t.Fatalf("tools/list = %+v, %v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "generate_image", Arguments: map[string]any{"prompt": "a stdio cat", "n": 1},
	})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %+v", result)
	}
	var text string
	for _, content := range result.Content {
		if part, ok := content.(*mcp.TextContent); ok {
			text += part.Text
		}
	}
	if !strings.Contains(text, "a clearer cat") || !strings.Contains(text, outputDir) {
		t.Fatalf("tool text = %q", text)
	}
	files, err := filepath.Glob(filepath.Join(outputDir, "*.png"))
	if err != nil || len(files) != 1 {
		t.Fatalf("generated files = %v, %v; text=%q", files, err, text)
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !bytes.Equal(data, png) {
		t.Fatalf("generated data = %x, %v", data, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if stderr.String() != "" {
		t.Fatalf("unexpected MCP stderr: %s", stderr.String())
	}
}
