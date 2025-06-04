/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/brimstone/ollamacli/list"
	"github.com/brimstone/ollamacli/prompt"
	"github.com/brimstone/ollamacli/version"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "ollamacli",
	Short: "A brief description of your application",
	Long: `A longer description that spans multiple lines and likely contains
examples and usage of using your application. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	//RunE: prompt.Run,
}

func main() {
	var programLevel = new(slog.LevelVar) // Info by default
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: programLevel})
	slog.SetDefault(slog.New(h))

	// check for environment variable now
	if os.Getenv("DEBUG") != "" {
		programLevel.Set(slog.LevelDebug)
	}
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.ollamacli.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	//rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

	rootCmd.AddCommand(prompt.Cmd())
	rootCmd.AddCommand(list.Cmd())
	rootCmd.AddCommand(version.Cmd())

	ctx := context.Background()

	cmd, _, _ := rootCmd.Find(os.Args[1:])
	//fmt.Println(cmd.Use, rootCmd.Use)
	// default cmd if no cmd is given
	if (len(os.Args) > 1 &&
		os.Args[1] != "completion" &&
		os.Args[1] != "__complete") &&
		cmd.Use == rootCmd.Use && cmd.Flags().Parse(os.Args[1:]) != pflag.ErrHelp {
		slog.Debug("Defaulting to prompt command")
		args := []string{"prompt"}
		args = append(args, os.Args[1:]...)
		rootCmd.SetArgs(args)
	}

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
