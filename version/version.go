// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package version

import (
	"fmt"

	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = "main"
	Date    = "???"
	Binary  = "clank"
)

func Cmd() *cobra.Command {
	var versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Application version",
		Long:  `Display the version of clank`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s %s built on %s\n", Binary, Version, Date)

			client, err := api.ClientFromEnvironment()
			if err == nil {
				version, err := client.Version(cmd.Context())
				if err == nil {
					fmt.Printf("Ollama version: %#v\n", version)
				}
			}
		},
	}

	return versionCmd
}
