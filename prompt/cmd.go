// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package prompt

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
)

func Cmd() *cobra.Command {
	var promptCmd = &cobra.Command{
		Use:   "prompt",
		Short: "Prompt a model",
		Long:  `Process prompting the LLM with any system or user prompts specified by the command line. This also handles calling any tools if the LLM requires.`,
		RunE:  Run,
	}

	promptCmd.Flags().StringP("model", "m", "", "Model to use")
	promptCmd.Flags().StringP("user", "u", "", "User prompt")

	err := promptCmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		client, err := api.ClientFromEnvironment()
		if err != nil {
			slog.Error("Unable to create client",
				"err", err,
			)

			return nil, cobra.ShellCompDirectiveError
		}

		imagePaths, err := cmd.Flags().GetStringSlice("image")
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		toolPaths, err := cmd.Flags().GetStringSlice("tool")
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		models, err := getValidModels(cmd.Context(), client, imagePaths, toolPaths)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		var m []string
		for _, n := range models {
			m = append(m, n.Name)
		}

		return m, 0
	})
	if err != nil {
		panic(err)
	}

	promptCmd.Flags().String("prefix", "", "Required prefix for response")
	promptCmd.Flags().StringP("system", "s", "", "System prompt")
	promptCmd.Flags().StringP("template", "t", "", "Template for system and user prompts")
	promptCmd.Flags().StringSlice("tool", nil, "URL or command for MCP tool (format http://... for streamable, sse+http://... for SSE)")

	err = promptCmd.RegisterFlagCompletionFunc("template", func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var t []string

		// Check files on disk
		files, err := os.ReadDir(filepath.Join(userHomeDir(), ".clank", "templates"))
		if err == nil {
			for _, f := range files {
				if f.IsDir() {
					// Check if system.md exists in this directory
					systemMdPath := filepath.Join(userHomeDir(), ".clank", "templates", f.Name(), "system.md")
					if _, err := os.Stat(systemMdPath); err == nil {
						t = append(t, f.Name())
					}
				}
			}
		}

		files, err = templates.ReadDir("templates")
		if err != nil {
			slog.Error("Unable to read internal templates",
				"err", err,
			)

			return nil, cobra.ShellCompDirectiveError
		}

		for _, f := range files {
			t = append(t, f.Name())
		}

		return t, 0
	})
	if err != nil {
		panic(err)
	}

	promptCmd.Flags().StringSliceP("image", "i", nil, "Images to include in the user prompt")
	promptCmd.Flags().Bool("unload", false, "Unload the model immediately after generation")

	return promptCmd
}
