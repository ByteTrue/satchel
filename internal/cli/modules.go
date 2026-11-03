package cli

import (
	"errors"
	"fmt"
	"io"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
)

func currentModules(cfg *config.Config) mcpserver.ModuleState {
	return mcpserver.ModuleState{
		Vision: cfg.Tools.ImageAsk, Image: cfg.Tools.GenerateImage,
		WebSearch: cfg.Tools.WebSearch && cfg.Tools.WebFetch,
	}
}

func RunModules(args []string, out io.Writer) error {
	if len(args) == 0 {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		state := currentModules(cfg)
		for _, module := range []mcpserver.Module{mcpserver.ModuleVision, mcpserver.ModuleImage, mcpserver.ModuleWebSearch} {
			fmt.Fprintf(out, "%s %s\n", module, onOff(state.Enabled(module)))
		}
		return nil
	}
	if len(args) != 2 {
		return errors.New("usage: satchel modules [vision|image|web-search on|off]")
	}
	var enabled bool
	switch args[1] {
	case "on":
		enabled = true
	case "off":
		enabled = false
	default:
		return errors.New("module state must be on or off")
	}
	state, err := setModule(mcpserver.Module(args[0]), enabled)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s\nRestart MCP clients for this change to take effect.\n", args[0], onOff(state.Enabled(mcpserver.Module(args[0]))))
	return nil
}

func setModule(module mcpserver.Module, enabled bool) (mcpserver.ModuleState, error) {
	cfg, err := config.Load()
	if err != nil {
		return mcpserver.ModuleState{}, err
	}
	switch module {
	case mcpserver.ModuleVision:
		cfg.Tools.ImageAsk = enabled
	case mcpserver.ModuleImage:
		cfg.Tools.GenerateImage = enabled
	case mcpserver.ModuleWebSearch:
		cfg.Tools.WebSearch = enabled
		cfg.Tools.WebFetch = enabled
	default:
		return mcpserver.ModuleState{}, errors.New("module must be vision, image, or web-search")
	}
	if err := config.Save(cfg); err != nil {
		return mcpserver.ModuleState{}, err
	}
	return currentModules(cfg), nil
}
