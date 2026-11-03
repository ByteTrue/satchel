package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"satchel/internal/config"
)

func TestSatchelCLIModulesApplyAfterStdioRestart(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	cfg := config.Default()
	cfg.Tools.GenerateImage = false
	cfg.Tools.WebSearch = false
	cfg.Tools.WebFetch = false
	if err := config.Save(cfg); err != nil {
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

	runCLI := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, binary, args...)
		command.Env = append(os.Environ(), "SATCHEL_CONFIG="+configPath)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("satchel %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	listTools := func() []string {
		t.Helper()
		command := exec.Command(binary, "mcp")
		command.Env = append(os.Environ(), "SATCHEL_CONFIG="+configPath)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		client := mcp.NewClient(&mcp.Implementation{Name: "satchel-cli-modules", Version: "0"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
		if err != nil {
			t.Fatalf("connect stdio: %v; stderr=%s", err, stderr.String())
		}
		list, err := session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tool := range list.Tools {
			if strings.HasPrefix(tool.Name, "satchel_") {
				t.Errorf("agent can see management tool %q", tool.Name)
			}
			names = append(names, tool.Name)
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		return names
	}

	if output := runCLI("modules", "vision", "off"); !strings.Contains(output, "vision off") {
		t.Fatalf("CLI output = %q", output)
	}
	if names := listTools(); len(names) != 0 {
		t.Fatalf("tools after CLI vision off = %v", names)
	}
	if output := runCLI("modules", "vision", "on"); !strings.Contains(output, "Restart MCP clients") {
		t.Fatalf("CLI output = %q", output)
	}
	if names := listTools(); len(names) != 1 || names[0] != "analyze_image" {
		t.Fatalf("tools after CLI vision on and restart = %v", names)
	}
}
