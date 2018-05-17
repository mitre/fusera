package cmd

import (
	"fmt"
	"os"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/fuseralib"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(unmountCmd)
}

var unmountCmd = &cobra.Command{
	Use:   "unmount /path/to/mountpoint",
	Short: "Unmount a running instance of Fusera.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		twig.Debug("got unmount command")
		twig.Debug("args:")
		twig.Debug(args)
		path := args[0]
		err := fuseralib.TryUnmount(path)
		if err != nil {
			fmt.Printf("Failed to unmount %s\n. Retry with --debug to see more information.", path)
			twig.Debugf("%+#v\n", err.Error())
			os.Exit(1)
		}
		twig.Debugf("Successfully unmounted %s", path)
	},
}
