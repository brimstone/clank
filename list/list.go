// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package list

import (
	"fmt"

	"log/slog"

	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
)

func Cmd() *cobra.Command {
	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "List models available for prompting",
		Long:  `Query the LLM provider for available models.`,
		RunE:  Run,
	}

	return listCmd
}

func Run(cmd *cobra.Command, args []string) error {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		slog.Error("Unable to create client",
			"err", err,
		)

		return err
	}

	models, err := client.List(cmd.Context())
	if err != nil {
		return err
	}

	for _, m := range models.Models {
		model, err := client.Show(cmd.Context(), &api.ShowRequest{
			Model: m.Name,
		})
		if err != nil {
			slog.Error("error showing models",
				"err", err,
			)

			continue
		}

		fmt.Printf("Model: %s %q\n", m.Name, model.Capabilities)
	}

	return nil
}
