// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package tool

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/brimstone/clank/utils"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func Cmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "tool",
		Short: "Interact with MCP server",
		Long:  `Interact with MCP servers directly`,
		RunE:  Run,
	}

	cmd.PersistentFlags().String("tool", "", "URL or command for MCP tool (format http://... for streamable, sse+http://... for SSE)")

	// Call Command
	var callCmd = &cobra.Command{
		Use:  "call",
		RunE: CallRun,
	}

	callCmd.Flags().String("function", "", "Call a specific function")

	cmd.AddCommand(callCmd)

	return cmd
}

func Run(cmd *cobra.Command, args []string) error {
	toolPath, err := cmd.Flags().GetString("tool")
	if err != nil {
		return err
	}

	if toolPath == "" {
		return errors.New("tool must be specified")
	}

	_, toolFuncs, err := utils.GetToolsFromPath(cmd.Context(), toolPath)
	if err != nil {
		return err
	}

	for _, f := range toolFuncs {
		fmt.Printf("Funcs: %s: %q\n", f.Function.Name, f.Function.Description)
		fmt.Printf("Parameters:\n")

		for k, v := range f.Function.Parameters.Properties.ToMap() {
			fmt.Printf("- %s: %s %t\n", k, v.Type.String(), slices.Contains(f.Function.Parameters.Required, k))
		}
	}

	return nil
}

func CallRun(cmd *cobra.Command, args []string) error {
	toolPath, err := cmd.Flags().GetString("tool")
	if err != nil {
		return err
	}

	if toolPath == "" {
		return errors.New("tool must be specified")
	}

	funcName, err := cmd.Flags().GetString("function")
	if err != nil {
		return err
	}

	if funcName == "" {
		return errors.New("function must be specified")
	}
	// Call the function
	mcpClient, _, err := utils.GetToolsFromPath(cmd.Context(), toolPath)
	if err != nil {
		return err
	}

	arguments := make(map[string]any)

	for _, arg := range args {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			slog.Warn("Unable to use argument", "argument", arg)

			continue
		}

		arguments[parts[0]] = parts[1]
	}

	toolRes, err := mcpClient.CS.CallTool(cmd.Context(), &mcp.CallToolParams{
		Name:      funcName,
		Arguments: arguments,
	})

	if err != nil {
		return err
	}

	for _, content := range toolRes.Content {
		switch tc := content.(type) {
		case *mcp.TextContent:
			fmt.Printf("Response: %s\n", tc.Text)
		}
	}

	return nil
}
