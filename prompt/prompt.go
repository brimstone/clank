package prompt

import (
	"context"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

//go:embed all:templates/*
var templates embed.FS

// Lifted from viper's source.
func userHomeDir() string {
	if runtime.GOOS == "windows" {
		home := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}

		return home
	}

	return os.Getenv("HOME")
}

func getValidModels(ctx context.Context, client *api.Client, imagePaths, toolPaths []string) ([]string, error) {
	modelsList, err := client.List(ctx)
	if err != nil {
		return nil, err
	}
	var models []string

	// Sort models by size, largest on top
	sort.Slice(modelsList.Models, func(i, j int) bool {
		return modelsList.Models[i].Size > modelsList.Models[j].Size
	})

	for _, m := range modelsList.Models {
		// TODO consider tool support
		if len(imagePaths) == 0 && len(toolPaths) == 0 {
			models = append(models, m.Name)
			continue
		}
		model, err := client.Show(ctx, &api.ShowRequest{
			Model: m.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("error showing models")
		}
		if len(imagePaths) > 0 && !slices.Contains(model.Capabilities, "vision") {
			continue
		}
		if len(toolPaths) > 0 && !slices.Contains(model.Capabilities, "tools") {
			continue
		}
		models = append(models, m.Name)
	}
	return models, nil
}

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

		return models, 0
	})
	promptCmd.Flags().String("prefix", "", "Required prefix for response")
	promptCmd.Flags().StringP("system", "s", "", "System prompt")
	promptCmd.Flags().StringP("template", "t", "", "Template for system and user prompts")
	promptCmd.Flags().StringSlice("tool", nil, "URL or command for MCP tool")
	promptCmd.RegisterFlagCompletionFunc("template", func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var t []string

		// Check files on disk
		files, err := os.ReadDir(filepath.Join(userHomeDir(), ".ollamacli", "templates"))
		if err == nil {
			for _, f := range files {
				// TODO check that there's at least a system.md in this directory
				t = append(t, f.Name())
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
	promptCmd.Flags().StringSliceP("image", "i", nil, "Images to include in the user prompt")
	promptCmd.Flags().Bool("unload", false, "Unload the model immediately after generation")

	return promptCmd
}

func Run(cmd *cobra.Command, args []string) error {

	// Setup ollama client
	client, err := api.ClientFromEnvironment()
	if err != nil {
		slog.Error("Unable to create client",
			"err", err,
		)
		return err
	}

	imagePaths, err := cmd.Flags().GetStringSlice("image")
	if err != nil {
		return err
	}

	toolPaths, err := cmd.Flags().GetStringSlice("tool")
	if err != nil {
		return err
	}

	models, err := getValidModels(cmd.Context(), client, imagePaths, toolPaths)
	if err != nil {
		return err
	}

	// Figure out which model to use
	model, err := cmd.Flags().GetString("model")
	if err != nil {
		return err
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

	// Start collecting messages for the model
	var messages []api.Message

	var systemPrompt string
	// If a template is called, try to find it in the embedded file system
	templateName, err := cmd.Flags().GetString("template")
	if templateName != "" {
		// Look for the template on disk first
		systemPromptBytes, err := os.ReadFile(filepath.Join(userHomeDir(), ".ollamacli", "templates", templateName, "system.md"))
		if err != nil {
			// Then look internal
			systemPromptBytes, err = templates.ReadFile("templates/" + templateName + "/system.md")
		}
		if err != nil {
			return fmt.Errorf("can't find template named %s", templateName)
		}
		systemPrompt = string(systemPromptBytes)
	}

	// Add system message, if it exists
	if systemPrompt == "" {
		systemPrompt, err = cmd.Flags().GetString("system")
		if err != nil {
			return err
		}
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

	var images []api.ImageData
	for _, p := range imagePaths {
		var d api.ImageData
		d, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		images = append(images, d)
	}

	//if userPrompt != "" {
	messages = append(messages, api.Message{
		Role:    "user",
		Content: userPrompt,
		Images:  images,
	})
	//}

	prefix, err := cmd.Flags().GetString("prefix")
	if err != nil {
		return err
	}

	unload, err := cmd.Flags().GetBool("unload")
	if err != nil {
		return err
	}
	// Pick a model and start the ChatRequest object
	req := &api.ChatRequest{
		Model:    model,
		Messages: messages,
	}

	if unload {
		req.KeepAlive = &api.Duration{0}
	}

	var content string

	var thinking bool
	respFunc := func(resp api.ChatResponse) error {
		if resp.Message.Content == "<think>" {
			thinking = true
		}

		if !thinking {
			content += resp.Message.Content
			// TODO handle <think> models
			if prefix != "" && len(content) > len(prefix) {
				if !strings.HasPrefix(content, prefix) {
					return fmt.Errorf("response failed to include required prefix")
				}
			}
		}
		// If it's not a standard terminal, don't worry about it
		if thinking {
			if resp.Message.Content == "</think>" {
				thinking = false
			}
			if term.IsTerminal(int(os.Stderr.Fd())) {
				if resp.Message.Content == "<think>" {
					// print code for gray text
					fmt.Fprintf(os.Stderr, "\x1b[38;2;128;128;128m")
					return nil
				} else if resp.Message.Content == "</think>" {
					fmt.Fprintf(os.Stderr, "\x1b[0m")
					return nil
				}
				fmt.Fprintf(os.Stderr, "%s", resp.Message.Content)
				return nil
			}
		}

		fmt.Printf("%s", resp.Message.Content)

		return nil
	}

	err = client.Chat(cmd.Context(), req, respFunc)
	fmt.Println()
	return err
}
