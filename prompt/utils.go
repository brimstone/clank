// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"

	"github.com/brimstone/clank/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ollama/ollama/api"
)

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
	case "memory":
		cs, err = mcpClientClient.Connect(ctx, s.Transport, nil)
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

		required, ok := is["required"]
		if ok {
			for _, r := range required.([]any) {
				f.Parameters.Required = append(f.Parameters.Required, r.(string))
			}
		}

		props, ok := is["properties"].(map[string]any)
		if ok {
			for p, v := range props {
				vm := v.(map[string]any)

				var toolprops api.ToolProperty
				switch t := vm["type"].(type) {
				case string:
					toolprops.Type = []string{t}
				case []string:
					toolprops.Type = t
				}

				description, ok := vm["description"]
				if ok {
					toolprops.Description = description.(string)
				}
				f.Parameters.Properties.Set(p, toolprops)
			}
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
