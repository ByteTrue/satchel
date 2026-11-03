package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type mcpConfigEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

func RunMCPConfig(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("mcp-config takes no arguments")
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
	return writeMCPConfig(out, executable, env)
}

func writeMCPConfig(out io.Writer, executable string, env map[string]string) error {
	document := map[string]any{
		"mcpServers": map[string]mcpConfigEntry{
			"satchel": {Command: executable, Args: []string{"mcp"}, Env: env},
		},
	}
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}
