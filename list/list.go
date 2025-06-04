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
		Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
		RunE: Run,
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
		//if slices.Contains(model.Capabilities, "tools") {
		fmt.Printf("Model: %s %q\n", m.Name, model.Capabilities)
		//}
	}
	return nil
}
