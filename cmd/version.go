package cmd

import (
	"fmt"

	"github.com/mitre/fusera/info"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of Fusera",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Fusera -- %s\n", info.Version)
	},
}
