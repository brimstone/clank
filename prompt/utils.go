// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"

	"github.com/brimstone/clank/utils"
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

func getToolsFromClaude(ctx context.Context) ([]utils.MCPClient, []api.Tool, error) {
	var mcpClients []utils.MCPClient

	var toolFuncs []api.Tool

	var claudeConfig struct {
		McpServers map[string]utils.MCPServer `json:"mcpServers"`
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
		mcpClient, toolFunc, err := utils.SetupTool(ctx, name, s)
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
