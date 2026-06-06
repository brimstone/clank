// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/brimstone/clank/utils"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func Cmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "mcp",
		Short: "Interact with MCP server",
		Long:  `Interact with MCP servers directly`,
		RunE:  Run,
	}

	cmd.PersistentFlags().String("tool", "", "URL or command for MCP tool (format http://... for streamable, sse+http://... for SSE)")

	// Function Command
	var functionCmd = &cobra.Command{
		Use:   "function [name] [arg key=value ...]",
		Short: "Call a function on the MCP server",
		Long: `Call a function on the MCP server.

The first positional argument is the function to call.
Any following positional arguments are passed to the function as
key=value pairs.

By default, JSON responses are pretty-printed. Use --raw to print the
response as a single line, for use when piping to other commands.`,
		Args: cobra.MinimumNArgs(1),
		RunE: FunctionRun,
	}

	functionCmd.Flags().Bool("raw", false, "Print the response as-is on a single line")

	cmd.AddCommand(functionCmd)

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

	for i, f := range toolFuncs {
		fmt.Printf("Funcs: %s: %q\n", f.Function.Name, f.Function.Description)

		if len(f.Function.Parameters.Properties.ToMap()) > 0 {
			fmt.Printf("Parameters:\n")
		}

		for k, v := range f.Function.Parameters.Properties.ToMap() {
			fmt.Printf("- %s: %s %t\n", k, v.Type.String(), slices.Contains(f.Function.Parameters.Required, k))
		}

		if i < len(toolFuncs)-1 {
			fmt.Println()
		}
	}

	return nil
}

func FunctionRun(cmd *cobra.Command, args []string) error {
	toolPath, err := cmd.Flags().GetString("tool")
	if err != nil {
		return err
	}

	if toolPath == "" {
		return errors.New("tool must be specified")
	}

	if len(args) == 0 {
		return errors.New("function must be specified")
	}

	funcName := args[0]

	// Call the function
	mcpClient, _, err := utils.GetToolsFromPath(cmd.Context(), toolPath)
	if err != nil {
		return err
	}

	arguments := make(map[string]any)

	for _, arg := range args[1:] {
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

	raw, err := cmd.Flags().GetBool("raw")
	if err != nil {
		return err
	}

	return printResponse(cmd.OutOrStdout(), toolRes.Content, raw)
}

// printResponse renders a tool call result. With raw set, the text of each
// content item is written verbatim as a single line. Otherwise, text items
// that contain JSON are pretty-printed; other content types are written as
// their compact JSON wire payload.
func printResponse(w io.Writer, content []mcp.Content, raw bool) error {
	for _, item := range content {
		textContent, ok := item.(*mcp.TextContent)
		if !ok {
			if data, err := item.MarshalJSON(); err == nil {
				if _, err := fmt.Fprintln(w, string(data)); err != nil {
					return err
				}
			}

			continue
		}

		if raw {
			if _, err := fmt.Fprintln(w, textContent.Text); err != nil {
				return err
			}

			continue
		}

		if json.Valid([]byte(textContent.Text)) {
			var pretty bytes.Buffer

			if err := json.Indent(&pretty, []byte(textContent.Text), "", "  "); err == nil {
				if _, err := fmt.Fprintln(w, pretty.String()); err != nil {
					return err
				}

				continue
			}
		}

		if _, err := fmt.Fprintln(w, textContent.Text); err != nil {
			return err
		}
	}

	return nil
}
