package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
)

type mcpConfigEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

func RunMCPConfig(args []string, out io.Writer) error {
	split := false
	if len(args) > 1 {
		return errors.New("usage: satchel mcp-config [--unified|--split]")
	}
	if len(args) == 1 {
		switch args[0] {
		case "--split":
			split = true
		case "--unified":
		default:
			return errors.New("usage: satchel mcp-config [--unified|--split]")
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	env := make(map[string]string)
	if path := os.Getenv("SATCHEL_CONFIG"); path != "" {
		env["SATCHEL_CONFIG"] = path
	}
	return writeMCPConfig(out, split, executable, currentModules(cfg), env)
}

func writeMCPConfig(out io.Writer, split bool, executable string, modules mcpserver.ModuleState, env map[string]string) error {
	servers := make(map[string]mcpConfigEntry)
	entry := func(args ...string) mcpConfigEntry {
		return mcpConfigEntry{Command: executable, Args: args, Env: env}
	}
	if !split {
		servers["satchel"] = entry("mcp")
	} else {
		for _, item := range []struct {
			module mcpserver.Module
			name   string
		}{
			{module: mcpserver.ModuleVision, name: "satchel-vision"},
			{module: mcpserver.ModuleImage, name: "satchel-image"},
			{module: mcpserver.ModuleWebSearch, name: "satchel-web-search"},
		} {
			if modules.Enabled(item.module) {
				servers[item.name] = entry("mcp", string(item.module))
			}
		}
	}
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(map[string]any{"mcpServers": servers})
}
