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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/nr"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	debug bool

	location  string
	accession string
	ngcpath   string
	filetype  string

	endpoint string

	locationMsg  string = "Cloud provider and region where files should be located: [cloud.region].\nEnvironment Variable: [$DBGAP_LOCATION]"
	accessionMsg string = "A list of accessions to mount or path to cart file. [\"SRR123,SRR456\" | local/cart/file | https://<bucket>.<region>.s3.amazonaws.com/<cart/file>].\nEnvironment Variable: [$DBGAP_ACCESSION]"
	ngcMsg       string = "A path to an ngc file used to authorize access to accessions in DBGaP: [local/ngc/file | https://<bucket>.<region>.s3.amazonaws.com/<ngc/file>].\nEnvironment Variable: [$DBGAP_NGC]"
	filetypeMsg  string = "comma separated list of the only file types to copy.\nEnvironment Varible: [$DBGAP_FILETYPE]"
	endpointMsg  string = "ADVANCED: Change the endpoint used to communicate with NIH API.\nEnvironment Variable: [$DBGAP_ENDPOINT]"
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug output.")
	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))

	rootCmd.Flags().StringVarP(&location, "location", "l", "", locationMsg)
	viper.BindPFlag("location", rootCmd.Flags().Lookup("location"))

	rootCmd.Flags().StringVarP(&accession, "accession", "a", "", accessionMsg)
	viper.BindPFlag("accession", rootCmd.Flags().Lookup("accession"))

	rootCmd.Flags().StringVarP(&ngcpath, "ngc", "n", "", ngcMsg)
	viper.BindPFlag("ngc", rootCmd.Flags().Lookup("ngc"))

	rootCmd.Flags().StringVarP(&filetype, "filetype", "f", "", filetypeMsg)
	viper.BindPFlag("filetype", rootCmd.Flags().Lookup("filetype"))

	rootCmd.Flags().StringVarP(&endpoint, "endpoint", "e", "https://www.ncbi.nlm.nih.gov/Traces/sdl/1/retrieve", endpointMsg)
	viper.BindPFlag("endpoint", rootCmd.Flags().Lookup("endpoint"))

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
				return errors.New("Seems like something was wrong with the format of the filetype flag.")
			}
		}
		path := args[0]
		accs, err := nr.ResolveNames(endpoint, location, ngc, 1, resolvedAccessions)
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
				if filetype != "" {
					if _, ok := types[f.Type]; !ok {
						continue
					}
				}
				// Check available disk space and see if file is larger.
				// If so, print out error message saying such, refuse to use curl, and move on.
				var stat syscall.Statfs_t
				wd, err := os.Getwd()
				syscall.Statfs(wd, &stat)

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

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func setConfig() {
	// If debug flag gets set, print debug statements.
	twig.SetDebug(debug)
}

func foldEnvVarsIntoFlagValues() {
	resolveString("location", &location)
	resolveString("accession", &accession)
	resolveString("ngc", &ngcpath)
}

func resolveString(name string, value *string) {
	if value == nil {
		return
	}
	if !viper.IsSet(name) {
		env := viper.GetString(name)
		if env != "" {
			*value = env
		}
	}
}
