// Modifications Copyright 2018 The MITRE Corporation
// Authors: Matthew Bianchi
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"fmt"
	"os"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/info"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	debug bool
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug output. Mostly for developers.")
	if err := viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug")); err != nil {
		panic("INTERNAL ERROR: could not bind debug flag to debug environment variable")
	}

	rootCmd.PersistentFlags().BoolVarP(&flags.Silent, "silent", "s", false, flags.SilentMsg)
	if err := viper.BindPFlag("silent", mountCmd.Flags().Lookup("silent")); err != nil {
		panic("INTERNAL ERROR: could not bind silent flag to silent environment variable")
	}

	rootCmd.PersistentFlags().BoolVarP(&flags.Verbose, "verbose", "v", false, flags.VerboseMsg)
	if err := viper.BindPFlag("verbose", mountCmd.Flags().Lookup("verbose")); err != nil {
		panic("INTERNAL ERROR: could not bind verbose flag to verbose environment variable")
	}

	viper.SetEnvPrefix(flags.EnvPrefix)
	viper.AutomaticEnv()
	info.BinaryName = "fusera"
}

var rootCmd = &cobra.Command{
	Use:     info.BinaryName,
	Short:   "A FUSE interface to the NCBI Sequence Read Archive (SRA) - " + info.Version,
	Long:    ``,
	Version: info.Version,
}

// Execute runs the main command of fusera, which has no action of its own,
// so it evaluates which subcommand should be executed.
func Execute() {
	if os.Geteuid() == 0 {
		fmt.Println("Running Fusera as root is not supported. This causes problems with mounting the filesystem using FUSE.")
		os.Exit(1)
	}
	if err := rootCmd.Execute(); err != nil {
		prettyPrintError(err)
		os.Exit(1)
	}
}

func setConfig() {
	// If debug flag gets set, print debug statements.
	twig.SetDebug(debug)
	if flags.Silent {
		flags.Verbose = false
	}
}
