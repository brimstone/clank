package prompt

import (
	"context"
	"embed"
	"encoding/json"
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

	"github.com/brimstone/ollamacli/version"
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

func getToolsFromClaude(ctx context.Context) ([]mcpClient, []api.Tool, error) {
	var mcpClients []mcpClient
	var toolFuncs []api.Tool

	type mcpServer struct {
		URL  string `json:"url"`
		Type string `json:"type"`
	}

	var claudeConfig struct {
		McpServers map[string]mcpServer `json:"mcpServers"`
	}

	// parse the claude.js into a structure
	configPath := filepath.Join(userHomeDir(), ".claude.json")
	// First check if the config file exists
	if _, err := os.Stat(configPath); err != nil {
		return nil, nil, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, err
	}
	err = json.Unmarshal(data, &claudeConfig)
	if err != nil {
		return nil, nil, err
	}

	// TODO query easy mcpServer for functions
	for _, s := range claudeConfig.McpServers {
		mcpClientClient := mcp.NewClient(&mcp.Implementation{Name: "ollamacli", Version: version.Version}, nil)
		var cs *mcp.ClientSession
		var err error
		if s.Type == "sse" {
			cs, err = mcpClientClient.Connect(ctx, mcp.NewSSEClientTransport(s.URL, nil))
			if err != nil {
				return nil, nil, err
			}
		} else if s.Type == "http" {
			cs, err = mcpClientClient.Connect(ctx, mcp.NewStreamableClientTransport(s.URL, nil))
			if err != nil {
				return nil, nil, err
			}
		} else {
			return nil, nil, fmt.Errorf("mcp server type %s not handled", s.Type)
		}
		// Get functions from server

		tools, err := cs.ListTools(ctx, nil)
		if err != nil {
			return nil, nil, err
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
			f.Parameters.Required = tool.InputSchema.Required
			f.Parameters.Properties = make(map[string]api.ToolProperty)
			for p, v := range tool.InputSchema.Properties {
				f.Parameters.Properties[p] = api.ToolProperty{
					Type:        []string{v.Type},
					Description: v.Description,
				}
			}

			toolFuncs = append(toolFuncs, api.Tool{
				Function: f,
			})
			mcpC.Tools = append(mcpC.Tools, tool.Name)
		}
		mcpClients = append(mcpClients, mcpC)
	}
	// TODO add the functions to toolFuncs
	// TODO add the client connection to mcpClients
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
			return nil, fmt.Errorf("error showing models")
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

		var m []string
		for _, n := range models {
			m = append(m, n.Name)
		}
		return m, 0
	})
	promptCmd.Flags().String("prefix", "", "Required prefix for response")
	promptCmd.Flags().StringP("system", "s", "", "System prompt")
	promptCmd.Flags().StringP("template", "t", "", "Template for system and user prompts")
	promptCmd.Flags().StringSlice("tool", nil, "URL or command for MCP tool (format http://... for streamable, sse+http://... for SSE)")
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

	/*
		TODO this won't trigger because the models are already filtered based on cli arguments
		if len(imagePaths) > 0 && !slices.Contains(model.Capabilities, "vision") {
			return fmt.Errorf("model %s doesn't support vision", modelName)
		}
		if len(toolPaths) > 0 && !slices.Contains(model.Capabilities, "tools") {
			return fmt.Errorf("model %s doesn't support tools", modelName)
		}
	*/

	slog.Debug("model", "model", model.Name)

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

	// https://deadprogrammersociety.com/2025/03/calling-mcp-servers-the-hard-way.html
	var mcpClients []mcpClient
	var toolFuncs []api.Tool

	// TODO If the model picked supports tools, then add the tools found in $HOME/.claude.json
	slog.Debug("Model capabilities",
		"capabilities", model,
	)
	if slices.Contains(model.Capabilities, "tools") {
		slog.Debug("Need to add tools from claude now")
		mcpC, toolF, err := getToolsFromClaude(cmd.Context())
		if err != nil {
			return err
		}
		for _, m := range mcpC {
			mcpClients = append(mcpClients, m)
		}
		for _, t := range toolF {
			toolFuncs = append(toolFuncs, t)
		}
	}

	// add any tool options to messages
	for _, t := range toolPaths {
		var cs *mcp.ClientSession
		// determine if the toolpath is a command on the system, or a http(s?) SSE server
		mcpClientClient := mcp.NewClient(&mcp.Implementation{Name: "ollamacli", Version: version.Version}, nil)
		// If the URL starts with "http" we need to establish a network connection
		// to the MCP server.  The transport type is chosen dynamically – first
		// we try a stream‑based transport, and if that fails we fall back to
		// Server‑Sent Events (SSE).  If both attempts fail we surface the error
		// to the caller.
		if strings.HasPrefix(t, "http") {
			// Attempt to connect using a stream‑able client transport
			cs, err = mcpClientClient.Connect(cmd.Context(), mcp.NewStreamableClientTransport(t, nil))
			if err != nil {
				return err
			}
		} else if strings.HasPrefix(t, "sse+") {
			t, _ = strings.CutPrefix(t, "sse+")
			cs, err = mcpClientClient.Connect(cmd.Context(), mcp.NewSSEClientTransport(t, nil))
			if err != nil {
				return err
			}
		} else {
			spaceSplitter, err := splitter.NewSplitter(' ', splitter.DoubleQuotes)
			if err != nil {
				return err
			}

			toolCmd, err := spaceSplitter.Split(t)
			if err != nil {
				return err
			}
			mcpCmd := exec.Command(toolCmd[0], toolCmd[1:]...)
			//TODO add debug option mcpCmd.Stderr = os.Stderr
			cs, err = mcpClientClient.Connect(cmd.Context(), mcp.NewCommandTransport(mcpCmd))
			if err != nil {
				return err
			}
		}
		tools, err := cs.ListTools(cmd.Context(), nil)
		if err != nil {
			return err
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
			f.Parameters.Required = tool.InputSchema.Required
			f.Parameters.Properties = make(map[string]api.ToolProperty)
			for p, v := range tool.InputSchema.Properties {
				f.Parameters.Properties[p] = api.ToolProperty{
					Type:        []string{v.Type},
					Description: v.Description,
				}
			}

			toolFuncs = append(toolFuncs, api.Tool{
				Function: f,
			})
			mcpC.Tools = append(mcpC.Tools, tool.Name)
		}
		mcpClients = append(mcpClients, mcpC)
	}

	prefix, err := cmd.Flags().GetString("prefix")
	if err != nil {
		return err
	}

	unload, err := cmd.Flags().GetBool("unload")
	if err != nil {
		return err
	}

	// TODO add debug for this
	/*
		for _, m := range messages {
			fmt.Printf("%s: %v\n", m.Role, m.Content)
		}
	*/

	callAgain := false
	var content string
	var thinking bool
	respFunc := func(resp api.ChatResponse) error {
		if len(resp.Message.ToolCalls) > 0 {
			callAgain = true
			messages = append(messages, resp.Message)
			for _, c := range resp.Message.ToolCalls {
				// TODO add debug for this  fmt.Printf("message: %#v\n", resp)
				// TODO fmt.Printf("Calling function %s with %#v\n", c.Function.Name, c.Function.Arguments)
				for _, mcpC := range mcpClients {
					if slices.Contains(mcpC.Tools, c.Function.Name) {
						slog.Debug("Calling tool",
							"tool", c.Function.Name,
							"args", c.Function.Arguments,
						)
						toolRes, err := mcpC.CS.CallTool(cmd.Context(), &mcp.CallToolParams{
							Name:      c.Function.Name,
							Arguments: c.Function.Arguments,
						})
						if err != nil {
							return err
						}
						for _, content := range toolRes.Content {
							//TODO add debug for this fmt.Printf("tool res: %#v\n", content)
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

	for {
		// Pick a model and start the ChatRequest object
		req := &api.ChatRequest{
			Model:    model.Name,
			Messages: messages,
			Tools:    toolFuncs,
		}

		if unload {
			req.KeepAlive = &api.Duration{0}
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
