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
	"strconv"
	"syscall"
	"time"

	"github.com/mitre/fusera/awsutil"
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

	location  string
	accession string
	tokenpath string
	filetype  string

	endpoint             string
	awsBatch, awsDefault int = 0, 50
	gcpBatch, gcpDefault int = 0, 25
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug output.")
	if err := viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug")); err != nil {
		panic("INTERNAL ERROR: could not bind debug flag to debug environment variable")
	}

	rootCmd.Flags().StringVarP(&location, "location", "l", "", flags.LocationMsg)
	if err := viper.BindPFlag("location", rootCmd.Flags().Lookup("location")); err != nil {
		panic("INTERNAL ERROR: could not bind location flag to location environment variable")
	}

	rootCmd.Flags().StringVarP(&accession, "accession", "a", "", flags.AccessionMsg)
	if err := viper.BindPFlag("accession", rootCmd.Flags().Lookup("accession")); err != nil {
		panic("INTERNAL ERROR: could not bind accession flag to accession environment variable")
	}

	rootCmd.Flags().StringVarP(&tokenpath, "token", "t", "", flags.TokenMsg)
	if err := viper.BindPFlag("token", rootCmd.Flags().Lookup("token")); err != nil {
		panic("INTERNAL ERROR: could not bind token flag to token environment variable")
	}

	rootCmd.Flags().StringVarP(&filetype, "filetype", "f", "", flags.FiletypeMsg)
	if err := viper.BindPFlag("filetype", rootCmd.Flags().Lookup("filetype")); err != nil {
		panic("INTERNAL ERROR: could not bind filetype flag to filetype environment variable")
	}

	rootCmd.Flags().StringVarP(&endpoint, "endpoint", "e", "https://www.ncbi.nlm.nih.gov/Traces/sdl/1/retrieve", flags.EndpointMsg)
	if err := viper.BindPFlag("endpoint", rootCmd.Flags().Lookup("endpoint")); err != nil {
		panic("INTERNAL ERROR: could not bind endpoint flag to endpoint environment variable")
	}

	rootCmd.Flags().IntVarP(&awsBatch, "aws-batch", "", awsDefault, flags.AwsBatchMsg)
	if err := viper.BindPFlag("aws-batch", rootCmd.Flags().Lookup("aws-batch")); err != nil {
		panic("INTERNAL ERROR: could not bind aw-batch flag to aw-batch environment variable")
	}

	rootCmd.Flags().IntVarP(&gcpBatch, "gcp-batch", "", gcpDefault, flags.GcpBatchMsg)
	if err := viper.BindPFlag("gcp-batch", rootCmd.Flags().Lookup("gcp-batch")); err != nil {
		panic("INTERNAL ERROR: could not bind gcp-batch flag to gcp-batch environment variable")
	}

	viper.SetEnvPrefix("dbgap")
	viper.AutomaticEnv()
}

var rootCmd = &cobra.Command{
	Use:     "sracp",
	Short:   "A tool similar to cp that allows a user to download accessions - " + flags.Version,
	Long:    ``,
	Version: flags.Version,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		setConfig()
		foldEnvVarsIntoFlagValues()
		var token []byte
		if tokenpath != "" {
			token, err = flags.ResolveNgcFile(tokenpath)
			if err != nil {
				return err
			}
		}
		// Now resolveAccession's value
		var accs []string
		if accession != "" {
			accs, err = flags.ResolveAccession(accession)
			if err != nil {
				return err
			}
		}

		// Location takes longest if there's a failure, so validate it last.
		var platform *awsutil.Platform
		if location == "" {
			twig.Debug("Location is empty, attempting to resolve location")
			platform, err = flags.FindLocation()
			if err != nil {
				twig.Debug(err)
				return errors.New("no location: a location was not provided so sracp attempted to resolve the location itself, this feature is only supported when sracp is running on Amazon or Google's cloud platforms")
			}
		} else {
			twig.Debug("Location was manually set")
			platform, err = awsutil.NewManualPlatform(location)
			if err != nil {
				twig.Debug(err)
				fmt.Println(err)
				return err
			}
		}
		twig.Debugf("Platform: %v", platform)

		var types map[string]bool
		if filetype != "" {
			types, err = flags.ResolveFileType(filetype)
			if err != nil {
				return errors.Errorf("could not parse contents of filetype flag: %s", filetype)
			}
		}
		path := args[0]
		batch := flags.ResolveBatch(platform.Name, awsBatch, gcpBatch)

		var accessions []*fuseralib.Accession
		var location string
		if platform.IsGCP() {
			location = string(platform.InstanceToken[:])
		} else {
			location, err = flags.ResolveLocation()
			if err != nil {
				twig.Debug(err)
				fmt.Println(err)
				return errors.New("no location provided")
			}
		}
		client := sdl.NewEagerClient(endpoint, location, token, types)
		if debug {
			fmt.Printf("Communicating with SDL API at: %s\n", endpoint)
			fmt.Printf("Using token at: %s\n", tokenpath)
			fmt.Printf("Contents of token: %s\n", string(token[:]))
			fmt.Printf("Limiting file types to: %v\n", types)
			fmt.Printf("Giving cloud platform as: %s\n", string(platform.Name))
			fmt.Printf("Giving region as: %s\n", string(platform.Region[:]))
			fmt.Printf("Requesting accessions in batches of: %d\n", batch)
		}
		if accs == nil || len(accs) == 0 {
			aa, err := client.Retrieve(nil)
			if err != nil {
				fmt.Println(err.Error())
			} else {
				accessions = append(accessions, aa...)
			}
		} else {
			dot := batch
			i := 0
			for dot < len(accs) {
				aa, err := client.Retrieve(accs[i:dot])
				if err != nil {
					fmt.Println(err.Error())
					fmt.Println("List of accessions that failed in this batch:")
					fmt.Println(accs[i:dot])
				} else {
					accessions = append(accessions, aa...)
				}
				i = dot
				dot += batch
			}
			aa, err := client.Retrieve(accs[i:])
			if err != nil {
				fmt.Println(err.Error())
				fmt.Println("List of accessions that failed in this batch:")
				fmt.Println(accs[i:])
			} else {
				accessions = append(accessions, aa...)
			}
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
				// Defensive programming: if the API returns filetypes the user didn't want, still don't copy them.
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
				fileSize, err := strconv.ParseUint(f.Size, 10, 64)
				if err != nil {
					fmt.Printf("%s: %s: failed to parse file size in order to check if there's enough disk space to copy it. File size value was %s", a.ID, f.Name, f.Size)
					continue
				}
				totalFileSize += fileSize
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
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func foldEnvVarsIntoFlagValues() {
	flags.ResolveString("endpoint", &endpoint)
	flags.ResolveInt("aws-batch", &awsBatch)
	flags.ResolveInt("gcp-batch", &gcpBatch)
	flags.ResolveString("location", &location)
	flags.ResolveString("accession", &accession)
	flags.ResolveString("token", &tokenpath)
	flags.ResolveString("filetype", &filetype)
}

func setConfig() {
	// If debug flag gets set, print debug statements.
	twig.SetDebug(debug)
}
