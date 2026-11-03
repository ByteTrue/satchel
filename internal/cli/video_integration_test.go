package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestSatchelStdioSlowVideoGenerationResult(t *testing.T) {
	mp4 := []byte("\x00\x00\x00\x18ftypmp42satchel-video")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/videos":
			time.Sleep(22 * time.Second)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["model"] != "video-test" || body["prompt"] != "a satchel in motion" || body["seconds"] != "1" || body["size"] != "16:9" {
				t.Errorf("video create payload = %#v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"vid-1"}`)
		case "/v1/videos/vid-1":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"vid-1","status":"completed"}`)
		case "/v1/videos/vid-1/content":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(mp4)
		default:
			t.Errorf("unexpected video path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	cfg := config.Default()
	cfg.Tools.ImageAsk = false
	cfg.Tools.WebSearch = false
	cfg.Tools.WebFetch = false
	cfg.Video = config.VideoConfig{API: "openai-videos", BaseURL: provider.URL + "/v1", APIKey: "video-key", Model: "video-test", OutputDir: filepath.Join(root, "videos")}
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
	client := mcp.NewClient(&mcp.Implementation{Name: "satchel-video-e2e", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect stdio: %v; stderr=%s", err, stderr.String())
	}
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) != 3 {
		t.Fatalf("tools/list = %+v, %v", tools, err)
	}
	first, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "generate_video", Arguments: map[string]any{
		"prompt": "a satchel in motion", "seconds": 1, "size": "16:9", "path": cfg.Video.OutputDir,
	}})
	if err != nil || first.IsError {
		t.Fatalf("generate_video = %+v, %v", first, err)
	}
	structured, ok := first.StructuredContent.(map[string]any)
	if !ok || structured["status"] != "pending" || structured["id"] == "" {
		t.Fatalf("slow video did not return a pending id: %#v", first.StructuredContent)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "generation_result", Arguments: map[string]any{"id": structured["id"]}})
	if err != nil || result.IsError {
		t.Fatalf("generation_result = %+v, %v", result, err)
	}
	files, err := filepath.Glob(filepath.Join(cfg.Video.OutputDir, "*.mp4"))
	if err != nil || len(files) != 1 {
		t.Fatalf("video files = %v, %v; result=%s", files, err, mcpText(result))
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !bytes.Equal(data, mp4) {
		t.Fatalf("video bytes = %x, %v", data, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if stderr.String() != "" {
		t.Fatalf("unexpected MCP stderr: %s", stderr.String())
	}
}
