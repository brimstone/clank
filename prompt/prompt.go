// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package prompt

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/brimstone/clank/version"
	"github.com/go-andiamo/splitter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

type mcpClient struct {
	CS    *mcp.ClientSession
	Tools []string
}

type mcpServer struct {
	URL     string   `json:"url"`
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"env"`
}

func setupTool(ctx context.Context, name string, s mcpServer) (mcpClient, []api.Tool, error) {
	var toolFuncs []api.Tool

	mcpClientClient := mcp.NewClient(&mcp.Implementation{Name: "clank", Version: version.Version}, nil)

	var cs *mcp.ClientSession

	var err error

	switch s.Type {
	case "sse":
		cs, err = mcpClientClient.Connect(ctx,
			&mcp.SSEClientTransport{
				Endpoint: s.URL,
			}, nil)
		if err != nil {
			slog.Error("Unable to add tool",
				"name", name,
				"err", err.Error())

			return mcpClient{}, nil, nil
		}
	case "http":
		cs, err = mcpClientClient.Connect(ctx,
			&mcp.StreamableClientTransport{
				Endpoint: s.URL,
			}, nil)
		if err != nil {
			slog.Error("Unable to add tool",
				"name", name,
				"err", err.Error())

			return mcpClient{}, nil, nil
		}
	case "":
		cs, err = mcpClientClient.Connect(ctx, &mcp.CommandTransport{
			Command: exec.CommandContext(ctx, s.Command, s.Args...), //nolint:gosec
		}, nil)
		if err != nil {
			slog.Error("Unable to add tool",
				"name", name,
				"err", err.Error())

			return mcpClient{}, nil, nil
		}
	default:
		return mcpClient{}, nil, fmt.Errorf("mcp server type %s not handled", s.Type)
	}
	// Get functions from server

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		slog.Error("ListTools", "err", err.Error())

		return mcpClient{}, nil, err
	}

	mcpC := mcpClient{CS: cs}

	for _, tool := range tools.Tools {
		slog.Debug("Adding tool",
			"tool", tool.Name,
			"description", tool.Description,
		)

		var f api.ToolFunction
		f.Name = tool.Name
		f.Description = tool.Description
		f.Parameters.Properties = api.NewToolPropertiesMap()
		is := tool.InputSchema.(map[string]any)

		for _, r := range is["required"].([]any) {
			f.Parameters.Required = append(f.Parameters.Required, r.(string))
		}

		props := is["properties"].(map[string]any)
		for p, v := range props {
			vm := v.(map[string]any)
			f.Parameters.Properties.Set(p, api.ToolProperty{
				Type:        []string{vm["type"].(string)},
				Description: vm["description"].(string),
			})
		}

		toolFuncs = append(toolFuncs, api.Tool{
			Function: f,
		})

		mcpC.Tools = append(mcpC.Tools, tool.Name)
	}

	return mcpC, toolFuncs, nil
}

func getToolsFromClaude(ctx context.Context) ([]mcpClient, []api.Tool, error) {
	var mcpClients []mcpClient

	var toolFuncs []api.Tool

	var claudeConfig struct {
		McpServers map[string]mcpServer `json:"mcpServers"`
	}

	// parse the claude.js into a structure
	configPath := filepath.Join(userHomeDir(), ".claude.json")
	// First check if the config file exists
	if _, err := os.Stat(configPath); err != nil {
		slog.Debug(".claude.json not found")

		return nil, nil, nil //nolint:nilerr
	}

	data, err := os.ReadFile(configPath) //nolint:gosec
	if err != nil {
		return nil, nil, err
	}

	err = json.Unmarshal(data, &claudeConfig)
	if err != nil {
		return nil, nil, err
	}

	// Query mcpServer for functions
	for name, s := range claudeConfig.McpServers {
		mcpClient, toolFunc, err := setupTool(ctx, name, s)
		if err != nil {
			return nil, nil, err
		}

		mcpClients = append(mcpClients, mcpClient)
		toolFuncs = append(toolFuncs, toolFunc...)
	}

	return mcpClients, toolFuncs, nil
}

type modelInfo struct {
	Name         string
	Capabilities []string
}

func getValidModels(ctx context.Context, client *api.Client, imagePaths, toolPaths []string) ([]modelInfo, error) {
	modelsList, err := client.List(ctx)
	if err != nil {
		return nil, err
	}

	var models []modelInfo

	// Sort models by size, largest on top
	sort.Slice(modelsList.Models, func(i, j int) bool {
		return modelsList.Models[i].Size > modelsList.Models[j].Size
	})

	for _, m := range modelsList.Models {
		model, err := client.Show(ctx, &api.ShowRequest{
			Model: m.Name,
		})
		if err != nil {
			return nil, errors.New("error showing models")
		}

		if len(imagePaths) > 0 && !slices.Contains(model.Capabilities, "vision") {
			continue
		}

		if len(toolPaths) > 0 && !slices.Contains(model.Capabilities, "tools") {
			continue
		}

		m2 := modelInfo{Name: m.Name}
		for _, c := range model.Capabilities {
			m2.Capabilities = append(m2.Capabilities, string(c))
		}

		models = append(models, m2)
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

func Run(cmd *cobra.Command, args []string) error { //nolint:gocyclo,maintidx
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
	modelName, err := cmd.Flags().GetString("model")
	if err != nil {
		return err
	}

	var model modelInfo

	// If the user didn't specify a model by name, pick the top one
	if modelName == "" {
		model = models[0]
	} else {
		for _, m := range models {
			if modelName == m.Name {
				model = m
			}
		}
	}

	// If model is still empty, then error

	if model.Name == "" {
		return fmt.Errorf("unable to find model %s", modelName)
	}

	slog.Debug("model", "model", model.Name)

	// Start collecting messages for the model
	var messages []api.Message

	var systemPrompt string
	// If a template is called, try to find it in the embedded file system
	templateName, err := cmd.Flags().GetString("template")
	if err != nil {
		return err
	}

	if templateName != "" {
		// Look for the template on disk first
		systemPromptBytes, err := os.ReadFile(filepath.Join(userHomeDir(), ".clank", "templates", templateName, "system.md")) //nolint:gosec
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
			b, err := os.ReadFile(systemPrompt) //nolint:gosec
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
	// First priority is the -u option, if it's a file, use the contents
	userPrompt, err := cmd.Flags().GetString("user")
	if err != nil {
		return err
	}

	if userPrompt != "" {
		_, err := os.Stat(userPrompt)
		if err == nil {
			b, err := os.ReadFile(userPrompt) //nolint:gosec
			if err != nil {
				return fmt.Errorf("unable to read userPrompt file %w", err)
			}

			userPrompt = string(b)
		}
	}

	if len(args) > 0 {
		if userPrompt != "" {
			userPrompt += "\n"
		}

		if args[1] != "" {
			_, err := os.Stat(userPrompt)
			if err == nil {
				b, err := os.ReadFile(userPrompt) //nolint:gosec
				if err != nil {
					return fmt.Errorf("unable to read userPrompt file %w", err)
				}

				userPrompt += string(b)
			} else {
				userPrompt += args[0]
			}
		}
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

		d, err := os.ReadFile(p) //nolint:gosec
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

	// https://deadprogrammersociety.com/2025/03/calling-mcp-servers-the-hard-way.html
	var mcpClients []mcpClient

	var toolFuncs []api.Tool

	slog.Debug("Model capabilities",
		"capabilities", model,
	)

	// If the model picked supports tools, then add the tools found in $HOME/.claude.json
	if slices.Contains(model.Capabilities, "tools") {
		slog.Debug("Need to add tools from claude now")

		mcpC, toolF, err := getToolsFromClaude(cmd.Context())
		if err != nil {
			return err
		}

		mcpClients = append(mcpClients, mcpC...)

		toolFuncs = append(toolFuncs, toolF...)
	}

	// add any tool options to messages
	for _, t := range toolPaths {
		s := mcpServer{}
		if strings.HasPrefix(t, "http") {
			s.Type = "http"
			s.URL = t
		} else if strings.HasPrefix(t, "sse+") {
			s.Type = "sse"
			s.URL = t
		} else {
			spaceSplitter, err := splitter.NewSplitter(' ', splitter.DoubleQuotes)
			if err != nil {
				return err
			}

			toolCmd, err := spaceSplitter.Split(t)
			if err != nil {
				return err
			}

			s.Command = toolCmd[0]
			s.Args = toolCmd[1:]
		}

		mcpClient, toolFunc, err := setupTool(cmd.Context(), "", s)
		if err != nil {
			return err
		}

		mcpClients = append(mcpClients, mcpClient)
		toolFuncs = append(toolFuncs, toolFunc...)
	}

	prefix, err := cmd.Flags().GetString("prefix")
	if err != nil {
		return err
	}

	unload, err := cmd.Flags().GetBool("unload")
	if err != nil {
		return err
	}

	callAgain := false

	var content string

	var thinking bool

	respFunc := func(resp api.ChatResponse) error {
		if len(resp.Message.ToolCalls) > 0 {
			callAgain = true

			messages = append(messages, resp.Message)

			for _, c := range resp.Message.ToolCalls {
				for _, mcpC := range mcpClients {
					if slices.Contains(mcpC.Tools, c.Function.Name) {
						argsJson, err := c.Function.Arguments.MarshalJSON()
						if err != nil {
							return err
						}

						slog.Debug("Calling tool",
							"tool", c.Function.Name,
							"argsv", string(argsJson),
						)

						toolRes, err := mcpC.CS.CallTool(cmd.Context(), &mcp.CallToolParams{
							Name:      c.Function.Name,
							Arguments: c.Function.Arguments,
						})
						if err != nil {
							return err
						}

						for _, content := range toolRes.Content {
							switch tc := content.(type) {
							case *mcp.TextContent:
								messages = append(messages, api.Message{
									Role:    "tool",
									Content: tc.Text,
								})
							}
						}
					}
				}
			}

			return nil
		}

		if resp.Message.Content == "<think>" {
			thinking = true
		}

		if !thinking {
			content += resp.Message.Content
			if prefix != "" && len(content) > len(prefix) {
				if !strings.HasPrefix(content, prefix) {
					return errors.New("response failed to include required prefix")
				}
			}
			// If it's not a standard terminal, don't worry about it
		} else {
			if resp.Message.Content == "</think>" {
				thinking = false
			}

			if term.IsTerminal(int(os.Stderr.Fd())) {
				switch resp.Message.Content {
				case "<think>":
					// print code for gray text
					fmt.Fprintf(os.Stderr, "\x1b[38;2;128;128;128m")

					return nil
				case "</think>":
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

	for {
		// debug messages going to the model
		for _, m := range messages {
			slog.Debug("message",
				"role", m.Role,
				"content", m.Content,
			)
		}
		// Pick a model and start the ChatRequest object
		req := &api.ChatRequest{
			Model:    model.Name,
			Messages: messages,
			Tools:    toolFuncs,
		}

		if unload {
			req.KeepAlive = &api.Duration{Duration: 0}
		}

		err = client.Chat(cmd.Context(), req, respFunc)

		if !callAgain {
			break
		}

		callAgain = false
	}

	fmt.Println()

	return err
}
