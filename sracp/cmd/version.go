package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version string
)

func init() {
	rootCmd.AddCommand(versionCmd)
	version = "v0.0.7"
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of sracp",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("sracp -- %s\n", version)
	},
}
