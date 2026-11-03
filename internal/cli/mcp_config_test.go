package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"satchel/internal/mcpserver"
)

func TestWriteMCPConfigUnified(t *testing.T) {
	var out bytes.Buffer
	err := writeMCPConfig(&out, false, "/usr/local/bin/satchel", mcpserver.ModuleState{Vision: true, Image: true, WebSearch: true}, map[string]string{"SATCHEL_CONFIG": "/tmp/config.json"})
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
		t.Fatalf("unified servers = %+v", document.Servers)
	}
	entry := document.Servers["satchel"]
	if entry.Command != "/usr/local/bin/satchel" || len(entry.Args) != 1 || entry.Args[0] != "mcp" || entry.Env["SATCHEL_CONFIG"] != "/tmp/config.json" {
		t.Fatalf("unified entry = %+v", entry)
	}
	if !filepath.IsAbs(entry.Command) {
		t.Fatal("MCP command is not absolute")
	}
}

func TestWriteMCPConfigSplitAndDisabledModules(t *testing.T) {
	var out bytes.Buffer
	modules := mcpserver.ModuleState{Vision: true, Image: true, WebSearch: false}
	if err := writeMCPConfig(&out, true, "/opt/satchel", modules, nil); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Servers map[string]mcpConfigEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Servers) != 2 {
		t.Fatalf("split servers = %+v", document.Servers)
	}
	for name, module := range map[string]string{"satchel-vision": "vision", "satchel-image": "image"} {
		entry, ok := document.Servers[name]
		if !ok || len(entry.Args) != 2 || entry.Args[0] != "mcp" || entry.Args[1] != module {
			t.Errorf("split entry %q = %+v", name, entry)
		}
	}
	if _, found := document.Servers["satchel-web-search"]; found {
		t.Fatal("disabled web-search module was included")
	}
}
