// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package prompt

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-andiamo/splitter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

//go:embed all:templates/*
var templates embed.FS

type mcpClient struct {
	CS    *mcp.ClientSession
	Tools []string
}

type mcpServer struct {
	URL       string                 `json:"url"`
	Type      string                 `json:"type"`
	Command   string                 `json:"command"`
	Args      []string               `json:"args"`
	Env       []string               `json:"env"`
	Transport *mcp.InMemoryTransport `json:"transport"` // for memory transports
}

type modelInfo struct {
	Name         string
	Capabilities []string
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

		if args[0] != "" {
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

		// Internal tools
		s := mcpServer{
			Type:      "memory",
			Transport: toolGetDate(cmd.Context()),
		}

		mcpClient, toolFunc, err := setupTool(cmd.Context(), "get_date", s)
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
							slog.Error("Error calling tool",
								"tool", c.Function.Name,
								"err", err,
							)

							return err
						}

						for _, content := range toolRes.Content {
							switch tc := content.(type) {
							case *mcp.TextContent:
								slog.Debug("Tool response",
									"response", tc.Text)

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
		} else { // If it's not a standard terminal, don't worry about it
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
			if m.Role == "tool" {
				for _, f := range m.ToolCalls {
					slog.Debug("message",
						"role", m.Role,
						"tool", f.Function.Name,
					)
				}
			} else {
				slog.Debug("message",
					"role", m.Role,
					"content", m.Content,
				)
			}
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
