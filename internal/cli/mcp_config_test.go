package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestWriteMCPConfigSingleUnifiedServer(t *testing.T) {
	var out bytes.Buffer
	err := writeMCPConfig(&out, "/usr/local/bin/satchel", map[string]string{"SATCHEL_CONFIG": "/tmp/config.json"})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Servers map[string]mcpConfigEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Servers) != 1 {
		t.Fatalf("servers = %+v", document.Servers)
	}
	entry, found := document.Servers["satchel"]
	if !found || entry.Command != "/usr/local/bin/satchel" || len(entry.Args) != 1 || entry.Args[0] != "mcp" || entry.Env["SATCHEL_CONFIG"] != "/tmp/config.json" {
		t.Fatalf("entry = %+v", entry)
	}
	if !filepath.IsAbs(entry.Command) {
		t.Fatal("MCP command is not absolute")
	}
}
