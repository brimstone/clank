package prompt

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
)

func Cmd() *cobra.Command {
	var promptCmd = &cobra.Command{
		Use:   "prompt",
		Short: "Prompt a model",
		Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
		RunE: Run,
	}
	promptCmd.Flags().StringP("model", "m", "", "Model to use")
	promptCmd.Flags().StringP("user", "u", "", "User prompt")
	promptCmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		client, err := api.ClientFromEnvironment()
		if err != nil {
			slog.Error("Unable to create client",
				"err", err,
			)
			return nil, cobra.ShellCompDirectiveError
		}

		modelsList, err := client.List(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var models []string

		sort.Slice(modelsList.Models, func(i, j int) bool {
			return modelsList.Models[i].Size > modelsList.Models[j].Size
		})

		for _, m := range modelsList.Models {
			// TODO consider tool support
			models = append(models, m.Name)
		}
		return models, 0
	})
	promptCmd.Flags().String("prefix", "", "Required prefix for response")
	promptCmd.Flags().StringP("system", "s", "", "System prompt")

	return promptCmd
}

func Run(cmd *cobra.Command, args []string) error {

	client, err := api.ClientFromEnvironment()
	if err != nil {
		slog.Error("Unable to create client",
			"err", err,
		)
		return err
	}

	model, err := cmd.Flags().GetString("model")
	if err != nil {
		return err
	}

	modelsList, err := client.List(cmd.Context())
	if err != nil {
		return err
	}
	var models []string

	sort.Slice(modelsList.Models, func(i, j int) bool {
		return modelsList.Models[i].Size > modelsList.Models[j].Size
	})

	for _, m := range modelsList.Models {
		// TODO consider tool support
		models = append(models, m.Name)
	}
	if model == "" {
		model = models[0]
	}

	if !slices.Contains(models, model) {
		// Search for the model in the list
		for _, m := range models {
			if strings.Contains(m, model) {
				model = m
			}
		}
		// If it still doesn't contain an available model, error
		if !slices.Contains(models, model) {
			return fmt.Errorf("unable to find model %s", model)
		}
	}

	slog.Debug("model", "model", model)
	var messages []api.Message

	// Add system message, if it exists
	systemPrompt, err := cmd.Flags().GetString("system")
	if err != nil {
		return err
	}
	if systemPrompt != "" {
		_, err := os.Stat(systemPrompt)
		if err == nil {
			b, err := os.ReadFile(systemPrompt)
			if err != nil {
				return fmt.Errorf("unable to read systemPrompt file %w", err)
			}
			systemPrompt = string(b)
		}
		messages = append(messages, api.Message{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	// Add prompt, if it exists
	userPrompt, err := cmd.Flags().GetString("user")
	if err != nil {
		return err
	}
	if len(args) > 0 {
		if userPrompt != "" {
			userPrompt += "\n"
		}
		userPrompt += args[0]
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		stdin, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		if userPrompt != "" {
			userPrompt += "\n"
		}
		userPrompt += string(stdin)
	}

	if userPrompt != "" {
		messages = append(messages, api.Message{
			Role:    "user",
			Content: userPrompt,
		})
	}

	prefix, err := cmd.Flags().GetString("prefix")
	if err != nil {
		return err
	}

	// Pick a model and start the ChatRequest object
	req := &api.ChatRequest{
		Model:    model,
		Messages: messages,
	}

	var content string

	respFunc := func(resp api.ChatResponse) error {
		content += resp.Message.Content
		// TODO handle <think> models
		if prefix != "" && len(content) > len(prefix) {
			if !strings.HasPrefix(content, prefix) {
				return fmt.Errorf("response failed to include required prefix")
			}
		}
		fmt.Print(resp.Message.Content)
		return nil
	}

	err = client.Chat(cmd.Context(), req, respFunc)
	fmt.Println()
	return err
}
