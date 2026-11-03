package cli

import (
	"context"
	"fmt"
	"io"
	"os"
)

var Version = "0.1.0-dev"

func Run(ctx context.Context, args []string, in *os.File, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		Usage(out)
		return 0
	}
	var err error
	switch args[0] {
	case "setup":
		if len(args) == 1 {
			err = RunSetup(in, out)
		} else {
			return usageError(errOut, "setup takes no arguments")
		}
	case "config":
		if len(args) == 2 && args[1] == "show" {
			err = ShowConfig(out, nil)
		} else {
			return usageError(errOut, "usage: satchel config show")
		}
	case "modules":
		err = RunModules(args[1:], out)
	case "mcp":
		if len(args) == 1 {
			err = RunMCP(ctx)
		} else {
			return usageError(errOut, "mcp takes no arguments")
		}
	case "mcp-config":
		err = RunMCPConfig(args[1:], out)
	case "version", "--version":
		if len(args) == 1 {
			fmt.Fprintf(out, "satchel %s\n", Version)
		} else {
			return usageError(errOut, "version takes no arguments")
		}
	default:
		return usageError(errOut, "unknown command "+args[0])
	}
	if err != nil {
		fmt.Fprintln(errOut, "satchel:", err)
		return 1
	}
	return 0
}

func Usage(out io.Writer) {
	fmt.Fprintln(out, `Satchel — a portable toolbox for AI agents

Usage:
  satchel setup        Configure providers and tool access
  satchel modules      List modules, or set: modules <module> <on|off>
  satchel config show  Show the redacted configuration
  satchel mcp          Run the unified stdio MCP server
  satchel mcp-config   Print the single-server MCP JSON
  satchel version      Print the version`)
}

func usageError(errOut io.Writer, message string) int {
	fmt.Fprintln(errOut, "satchel:", message)
	return 2
}
