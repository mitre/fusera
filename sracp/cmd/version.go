package cmd

import (
	"fmt"

	"github.com/mitre/fusera/flags"
	"github.com/spf13/cobra"
)

var (
	version string
)

func init() {
	rootCmd.AddCommand(versionCmd)
	version = "v0.0.9"
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of sracp",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("sracp -- %s\n", flags.Version)
	},
}
