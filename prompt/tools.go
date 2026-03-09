// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package prompt

import (
	"context"
	"log/slog"
	"time"

	"github.com/ijt/go-anytime"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func toolGetDate(ctx context.Context) *mcp.InMemoryTransport {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func(ctx context.Context) {
		server := mcp.NewServer(&mcp.Implementation{Name: "today"}, nil)

		type args struct {
			Relative string `json:"relative" jsonschema:"date relative to today"`
		}

		mcp.AddTool(server, &mcp.Tool{
			Name:        "get_date",
			Description: "Get today, tomorrow, or yesterday's date",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args args) (*mcp.CallToolResult, any, error) {
			targetDate, err := anytime.Parse(args.Relative, time.Now())
			if err != nil {
				slog.Error("Error parsing date", "relative", args.Relative, "err", err)

				return nil, nil, err
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: targetDate.Format("2006-01-02")},
				},
			}, nil, nil
		})

		if err := server.Run(ctx, serverTransport); err != nil {
			slog.Error("Unable to start get_date tool", "err", err)
		}
	}(ctx)

	return clientTransport
}
