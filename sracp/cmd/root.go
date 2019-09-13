// Copyright 2015 - 2017 Ka-Hing Cheung
// Copyright 2015 - 2017 Google Inc. All Rights Reserved.
// Modifications Copyright 2018 The MITRE Corporation
// Authors: Ka-Hing Cheung, Matthew Bianchi
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
	"path/filepath"
	"syscall"
	"time"

	"github.com/mitre/fusera/gps"
	"github.com/mitre/fusera/info"

	"github.com/mitre/fusera/fuseralib"
	"github.com/mitre/fusera/sdl"

	"github.com/cavaliercoder/grab"
	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/flags"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	debug bool
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug output.")
	if err := viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug")); err != nil {
		panic("INTERNAL ERROR: could not bind debug flag to debug environment variable")
	}

	rootCmd.Flags().StringVarP(&flags.Location, "location", "l", "", flags.LocationMsg)
	if err := viper.BindPFlag("location", rootCmd.Flags().Lookup("location")); err != nil {
		panic("INTERNAL ERROR: could not bind location flag to location environment variable")
	}

	rootCmd.Flags().StringVarP(&flags.Accession, "accession", "a", "", flags.AccessionMsg)
	if err := viper.BindPFlag("accession", rootCmd.Flags().Lookup("accession")); err != nil {
		panic("INTERNAL ERROR: could not bind accession flag to accession environment variable")
	}

	rootCmd.Flags().StringVarP(&flags.Tokenpath, "token", "t", "", flags.TokenMsg)
	if err := viper.BindPFlag("token", rootCmd.Flags().Lookup("token")); err != nil {
		panic("INTERNAL ERROR: could not bind token flag to token environment variable")
	}

	rootCmd.Flags().StringVarP(&flags.Filetype, "filetype", "f", "", flags.FiletypeMsg)
	if err := viper.BindPFlag("filetype", rootCmd.Flags().Lookup("filetype")); err != nil {
		panic("INTERNAL ERROR: could not bind filetype flag to filetype environment variable")
	}

	rootCmd.Flags().StringVarP(&flags.Endpoint, "endpoint", "e", "https://www.ncbi.nlm.nih.gov/Traces/sdl/1/retrieve", flags.EndpointMsg)
	if err := viper.BindPFlag("endpoint", rootCmd.Flags().Lookup("endpoint")); err != nil {
		panic("INTERNAL ERROR: could not bind endpoint flag to endpoint environment variable")
	}

	rootCmd.Flags().IntVarP(&flags.Batch, "batch", "", flags.BatchDefault, flags.BatchMsg)
	if err := viper.BindPFlag("batch", rootCmd.Flags().Lookup("batch")); err != nil {
		panic("INTERNAL ERROR: could not bind batch flag to batch environment variable")
	}

	viper.SetEnvPrefix("dbgap")
	viper.AutomaticEnv()

	info.BinaryName = "sracp"
}

var rootCmd = &cobra.Command{
	Use:     info.BinaryName,
	Short:   "A tool similar to cp that allows a user to download accessions - " + info.Version,
	Long:    ``,
	Version: info.Version,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		setConfig()
		flags.FoldEnvVarsIntoFlagValues()
		tokenpath := flags.FoldNgcIntoToken(flags.Tokenpath, flags.NgcPath)
		var token []byte
		if tokenpath != "" {
			token, err = flags.ResolveNgcFile(tokenpath)
			if err != nil {
				return err
			}
		}
		var accs []string
		if flags.Accession != "" {
			accs, err = flags.ResolveAccession(flags.Accession)
			if err != nil {
				return err
			}
		}
		var types map[string]bool
		if flags.Filetype != "" {
			types, err = flags.ResolveFileType(flags.Filetype)
			if err != nil {
				return err
			}
		}

		path := args[0]
		// Test whether we can write to this location. If not, fail here.
		err = os.MkdirAll(filepath.Join(path, ".test"), 0755)
		if err != nil {
			fmt.Printf("It seems like sracp cannot make directories under %s. Please check that you have correct permissions to write to that path.\n", path)
			os.Exit(1)
		}

		// Location takes longest if there's a failure, so validate it last.
		var locator gps.Locator
		if flags.Location != "" {
			locator, err = gps.NewManualLocation(flags.Location)
			if err != nil {
				twig.Debug(err)
				fmt.Println(err)
				return err
			}
		} else { // figure out which locator we'll need
			locator, err = gps.GenerateLocator()
			if err != nil {
				twig.Debug(err)
				fmt.Println(err)
				return errors.New("no location provided")
			}
		}

		info.LoadAccessionMap(accs)
		var API = sdl.NewSDL()
		var param = sdl.NewParam(accs, locator, token, sdl.SetAcceptCharges(flags.AwsProfile, flags.GcpProfile), types)
		API.Param = param
		API.URL = flags.Endpoint
		if flags.Verbose {
			fmt.Printf("Communicating with SDL API at: %s\n", flags.Endpoint)
			fmt.Printf("Using token at: %s\n", tokenpath)
			fmt.Printf("Contents of token: %s\n", string(token[:]))
			fmt.Printf("Limiting file types to: %v\n", types)
			fmt.Printf("Giving locality as: %s\n", locator.LocalityType())
			fmt.Printf("Requesting accessions in batches of: %d\n", flags.Batch)
		}
		accessions, warnings := fuseralib.FetchAccessions(API, accs, flags.Batch)
		if warnings != nil {
			if !flags.Silent {
				fmt.Println(err.Error())
			}
		}
		if len(accessions) == 0 {
			if !flags.Silent {
				fmt.Println("It seems like none of the accessions were successful, fusera is shutting down.")
			}
			os.Exit(1)
		}

		for _, a := range accessions {
			err := os.MkdirAll(filepath.Join(path, a.ID), 0755)
			if err != nil {
				fmt.Printf("Issue creating directory for %s: %s\n", a.ID, err.Error())
				continue
			}
			// create a batch of urls to download and collect combined file size to still do disk check.
			urls := make([]string, 0, len(accs))
			var totalFileSize uint64
			for _, f := range a.Files {
				// if the API returns filetypes the user didn't want, still don't copy them.
				if types != nil {
					if _, ok := types[f.Type]; !ok {
						continue
					}
				}
				if f.Link == "" {
					fmt.Printf("file: %s had no link, moving on to download other files\n", f.Name)
					continue
				}
				urls = append(urls, f.Link)
				totalFileSize += f.Size
			}
			// Check available disk space and see if file is larger.
			// If so, print out error message saying such, refuse to use curl, and move on.
			var stat syscall.Statfs_t
			wd, err := os.Getwd()
			if err := syscall.Statfs(wd, &stat); err != nil {
				return err
			}

			// Available blocks * size per block = available space in bytes
			availableBytes := stat.Bavail * uint64(stat.Bsize)
			if availableBytes < totalFileSize {
				fmt.Printf("DISK FULL: It appears there are only %d available bytes on disk and the batch of files in accession %s is %d bytes.", availableBytes, a.ID, totalFileSize)
				continue
			}

			respch, err := grab.GetBatch(0, filepath.Join(path, a.ID), urls...)
			if err != nil {
				twig.Debugf("%v\n", err)
			}
			// start a ticker to update progress every 200ms
			t := time.NewTicker(time.Second)

			// monitor downloads
			completed := 0
			inProgress := 0
			responses := make([]*grab.Response, 0)
			for completed < len(urls) {
				select {
				case resp := <-respch:
					// a new response has been received and has started downloading
					// (nil is received once, when the channel is closed by grab)
					if resp != nil {
						responses = append(responses, resp)
					}

				case <-t.C:

					// update completed downloads
					for i, resp := range responses {
						if resp != nil && resp.IsComplete() {
							// mark completed
							responses[i] = nil
							completed++
						}
					}

					// update downloads in progress
					inProgress = 0
					for _, resp := range responses {
						if resp != nil && !resp.IsComplete() {
							inProgress++
						}
					}
				}
			}

			t.Stop()

			fmt.Printf("accession %s finished: %d file(s) successfully downloaded.\n", a.ID, len(urls))
		}
		return nil
	},
}

// Execute runs the root command of sracp, which copies files from the cloud to a local file system.
func Execute() {
	if os.Geteuid() == 0 {
		fmt.Println("Running sracp as root is not supported. The tool should not require root.")
		os.Exit(1)
	}
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func setConfig() {
	// If debug flag gets set, print debug statements.
	twig.SetDebug(debug)
}
