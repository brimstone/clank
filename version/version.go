// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package version

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	Version string = "dev"
	Commit  string = "main"
	Date    string = ""
	Binary  string = "clank"
)

func Cmd() *cobra.Command {
	var versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Application version",
		Long:  `Display the version of clank`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s %s built on %s\n", Binary, Version, Date)
		},
	}

	return versionCmd
}
