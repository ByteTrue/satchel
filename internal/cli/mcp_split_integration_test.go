package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"satchel/internal/config"
)

func TestSatchelSplitMCPToolSurfaces(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "satchel")
	build := exec.Command("go", "build", "-o", binary, "./cmd/satchel")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build satchel: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, tc := range []struct {
		module string
		want   []string
	}{
		{module: "vision", want: []string{"analyze_image"}},
		{module: "image", want: []string{"generate_image", "generate_video", "generation_result"}},
		{module: "web-search", want: []string{"web_search", "web_fetch"}},
	} {
		t.Run(tc.module, func(t *testing.T) {
			command := exec.Command(binary, "mcp", tc.module)
			command.Env = append(os.Environ(), "SATCHEL_CONFIG="+configPath)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			client := mcp.NewClient(&mcp.Implementation{Name: "satchel-split-test", Version: "0"}, nil)
			session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
			if err != nil {
				t.Fatalf("connect: %v; stderr=%s", err, stderr.String())
			}
			list, err := session.ListTools(ctx, &mcp.ListToolsParams{})
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Tools) != len(tc.want) {
				t.Fatalf("tools = %+v, want %v", list.Tools, tc.want)
			}
			seen := make(map[string]bool)
			for _, tool := range list.Tools {
				seen[tool.Name] = true
			}
			for _, name := range tc.want {
				if !seen[name] {
					t.Errorf("missing tool %q in %+v", name, list.Tools)
				}
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
