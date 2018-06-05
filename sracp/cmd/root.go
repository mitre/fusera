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
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/nr"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	debug bool

	location  string
	accession string
	ngcpath   string
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

	rootCmd.Flags().StringVarP(&ngcpath, "ngc", "n", "", flags.NgcMsg)
	if err := viper.BindPFlag("ngc", rootCmd.Flags().Lookup("ngc")); err != nil {
		panic("INTERNAL ERROR: could not bind ngc flag to ngc environment variable")
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
	Short:   "",
	Long:    ``,
	Version: version,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		setConfig()
		twig.Debug("got sracp command")
		twig.Debug("args:")
		twig.Debug(args)
		foldEnvVarsIntoFlagValues()
		twig.Debug("location: " + location)
		twig.Debug("accessions: " + accession)
		var ngc []byte
		if ngcpath != "" {
			ngc, err = flags.ResolveNgcFile(ngcpath)
			if err != nil {
				return err
			}
		}
		if accession == "" {
			return errors.New("No accessions provided: sracp needs a list of accessions in order to know what files to copy.")
		}
		// Now resolveAccession's value
		resolvedAccessions, err := flags.ResolveAccession(accession)
		if err != nil {
			return err
		}

		// Location takes longest if there's a failure, so validate it last.
		if location == "" {
			location, err = flags.ResolveLocation()
			if err != nil {
				twig.Debug(err)
				return errors.New("No location: A location was not provided so sracp attempted to resolve the location itself. This feature is only supported when sracp is running on Amazon or Google's cloud platforms.")
			}
		}
		var types map[string]bool
		if filetype != "" {
			types, err = flags.ResolveFileType(filetype)
			if err != nil {
				return errors.Errorf("could not parse contents of filetype flag: %s", filetype)
			}
		}
		path := args[0]
		accs, err := nr.ResolveNames(endpoint, 25, false, location, ngc, resolvedAccessions, types)
		if err != nil {
			return err
		}
		_, err = exec.LookPath("curl")
		if err != nil {
			fmt.Println("Sracp cannot find the executable \"curl\" on the machine. Please install it and try again.")
			return err
		}
		for _, v := range accs {
			err := os.MkdirAll(filepath.Join(path, v.ID), 0755)
			if err != nil {
				fmt.Printf("Issue creating directory for %s: %s\n", v.ID, err.Error())
				continue
			}
			for _, f := range v.Files {
				// Defensive programming: if the API returns filetypes the user didn't want, still don't copy them.
				if types != nil {
					if _, ok := types[f.Type]; !ok {
						continue
					}
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
				fileSize, err := strconv.ParseUint(f.Size, 10, 64)
				if err != nil {
					fmt.Printf("%s: %s: failed to parse file size in order to check if there's enough disk space to copy it. File size value was %s", v.ID, f.Name, f.Size)
					continue
				}

				if availableBytes < fileSize {
					fmt.Printf("DISK FULL: It appears there are only %d available bytes on disk and the file %s is %d bytes.", availableBytes, f.Name, fileSize)
					continue
				}

				// TODO: call libcurl on each url to the path specified
				args := []string{"-o", filepath.Join(path, v.ID, f.Name), f.Link}
				cmd := exec.Command("curl", args...)
				cmd.Env = os.Environ()
				err = cmd.Run()
				if err != nil {
					twig.Infof("Issue copying %s: %s\n", args[2], err.Error())
				}
			}
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
	flags.ResolveString("ngc", &ngcpath)
	flags.ResolveString("filetype", &filetype)
}

func setConfig() {
	// If debug flag gets set, print debug statements.
	twig.SetDebug(debug)
}
