// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"strings"

	"github.com/brimstone/clank/version"
	"github.com/go-andiamo/splitter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ollama/ollama/api"
)

type slogWriter struct{}

func (slogWriter) Write(p []byte) (int, error) {
	slog.Debug(string(p))

	return len(p), nil
}

// warnOnUntrustedCert returns a TLS VerifyPeerCertificate hook that verifies
// the server certificate against the system root pool and logs a warning when
// it is untrusted. It never fails the handshake; InsecureSkipVerify is set
// separately so any certificate is accepted.
func warnOnUntrustedCert(host string) func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return nil
		}

		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			slog.Warn("Unable to parse server certificate",
				"endpoint", host,
				"err", err.Error(),
			)

			return nil
		}

		intermediates := x509.NewCertPool()

		for _, raw := range rawCerts[1:] {
			if cert, err := x509.ParseCertificate(raw); err == nil {
				intermediates.AddCert(cert)
			}
		}

		if _, err := leaf.Verify(x509.VerifyOptions{
			Intermediates: intermediates,
			DNSName:       host,
		}); err != nil {
			slog.Warn("Server certificate is not trusted by system certificate pool (accepting anyway)",
				"endpoint", host,
				"err", err.Error(),
			)
		}

		return nil
	}
}

type MCPClient struct {
	CS    *mcp.ClientSession
	Tools []string
}

type MCPServer struct {
	URL       string                 `json:"url"`
	Type      string                 `json:"type"`
	Command   string                 `json:"command"`
	Args      []string               `json:"args"`
	Env       []string               `json:"env"`
	Transport *mcp.InMemoryTransport `json:"transport"` // for memory transports
}

func SetupTool(ctx context.Context, name string, s MCPServer) (MCPClient, []api.Tool, error) {
	var toolFuncs []api.Tool

	mcpClientClient := mcp.NewClient(&mcp.Implementation{
		Name:    "clank",
		Version: version.Version,
	},
		&mcp.ClientOptions{
			ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				fmt.Printf("Got a ElicitRequest: %#v\n", req)

				return &mcp.ElicitResult{Action: "deny"}, nil
			},
		},
	)

	var cs *mcp.ClientSession

	var err error

	var transport mcp.Transport

	var httpClient *http.Client

	if strings.HasPrefix(s.URL, "https://") {
		endpoint, err := url.Parse(s.URL)
		if err != nil {
			return MCPClient{}, nil, err
		}

		host := endpoint.Hostname()

		if host == "" {
			host = "localhost"
		}

		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify:    true, //nolint:gosec
					VerifyPeerCertificate: warnOnUntrustedCert(host),
				},
			},
		}
	}

	switch s.Type {
	case "sse":
		transport = &mcp.SSEClientTransport{
			Endpoint:   s.URL,
			HTTPClient: httpClient,
		}
	case "http":
		transport = &mcp.StreamableClientTransport{
			Endpoint:   s.URL,
			HTTPClient: httpClient,
		}
	case "memory":
		transport = s.Transport
	case "":
		cmd := exec.CommandContext(ctx, s.Command, s.Args...) //nolint:gosec
		transport = &mcp.CommandTransport{
			Command: cmd,
		}
	default:
		return MCPClient{}, nil, fmt.Errorf("mcp server type %s not handled", s.Type)
	}

	// FIXME account for not debugging
	//transport = &mcp.LoggingTransport{Transport: transport, Writer: os.Stderr}
	cs, err = mcpClientClient.Connect(ctx, transport, nil)
	if err != nil {
		slog.Error("Unable to add tool",
			"name", name,
			"err", err.Error())

		return MCPClient{}, nil, err
	}

	// Get functions from server
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		slog.Error("ListTools", "err", err.Error())

		return MCPClient{}, nil, err
	}

	mcpC := MCPClient{CS: cs}

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

func GetToolsFromPath(ctx context.Context, toolPath string) (MCPClient, []api.Tool, error) {
	s := MCPServer{}
	if strings.HasPrefix(toolPath, "http") {
		s.Type = "http"
		s.URL = toolPath
	} else if strings.HasPrefix(toolPath, "sse+") {
		s.Type = "sse"
		s.URL = toolPath
	} else {
		spaceSplitter, err := splitter.NewSplitter(' ', splitter.DoubleQuotes)
		if err != nil {
			return MCPClient{}, nil, err
		}

		toolCmd, err := spaceSplitter.Split(toolPath)
		if err != nil {
			return MCPClient{}, nil, err
		}

		s.Command = toolCmd[0]
		s.Args = toolCmd[1:]
	}

	mcpClient, toolFunc, err := SetupTool(ctx, "", s)
	if err != nil {
		return MCPClient{}, nil, err
	}

	return mcpClient, toolFunc, nil
}
