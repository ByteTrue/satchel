package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
)

func TestSetModulePersistsGroupSwitches(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	state, err := setModule(mcpserver.ModuleWebSearch, false)
	if err != nil || state.WebSearch {
		t.Fatalf("set web-search = %+v, %v", state, err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.WebSearch || cfg.Tools.WebFetch {
		t.Fatalf("web module was not persisted as a group: %+v", cfg.Tools)
	}
	state, err = setModule(mcpserver.ModuleVision, false)
	if err != nil || state.Vision || !state.Image {
		t.Fatalf("set vision = %+v, %v", state, err)
	}
	cfg, err = config.Load()
	if err != nil || cfg.Tools.ImageAsk || !cfg.Tools.GenerateImage {
		t.Fatalf("vision/image state = %+v, %v", cfg.Tools, err)
	}
}

func TestRunModulesListsAndChangesState(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunModules(nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vision on", "image on", "web-search on"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("module list %q does not contain %q", out.String(), want)
		}
	}
	out.Reset()
	if err := RunModules([]string{"vision", "off"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "vision off") || !strings.Contains(out.String(), "Restart MCP clients") {
		t.Fatalf("set output = %q", out.String())
	}
	cfg, err := config.Load()
	if err != nil || cfg.Tools.ImageAsk {
		t.Fatalf("CLI did not persist vision off: %+v, %v", cfg.Tools, err)
	}
	if err := RunModules([]string{"vision", "sometimes"}, &out); err == nil {
		t.Fatal("invalid module state was accepted")
	}
}
