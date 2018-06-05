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
	"context"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/fuseralib"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	location  string
	accession string
	ngcpath   string
	filetype  string

	endpoint             string
	awsBatch, awsDefault int = 0, 50
	gcpBatch, gcpDefault int = 0, 25
)

func init() {
	mountCmd.Flags().StringVarP(&location, "location", "l", "", flags.LocationMsg)
	if err := viper.BindPFlag("location", mountCmd.Flags().Lookup("location")); err != nil {
		panic("INTERNAL ERROR: could not bind location flag to location environment variable")
	}

	mountCmd.Flags().StringVarP(&accession, "accession", "a", "", flags.AccessionMsg)
	if err := viper.BindPFlag("accession", mountCmd.Flags().Lookup("accession")); err != nil {
		panic("INTERNAL ERROR: could not bind accession flag to accession environment variable")
	}

	mountCmd.Flags().StringVarP(&ngcpath, "ngc", "n", "", flags.NgcMsg)
	if err := viper.BindPFlag("ngc", mountCmd.Flags().Lookup("ngc")); err != nil {
		panic("INTERNAL ERROR: could not bind ngc flag to ngc environment variable")
	}

	mountCmd.Flags().StringVarP(&filetype, "filetype", "f", "", flags.FiletypeMsg)
	if err := viper.BindPFlag("filetype", mountCmd.Flags().Lookup("filetype")); err != nil {
		panic("INTERNAL ERROR: could not bind filetype flag to filetype environment variable")
	}

	mountCmd.Flags().StringVarP(&endpoint, "endpoint", "e", "https://www.ncbi.nlm.nih.gov/Traces/sdl/1/retrieve", flags.EndpointMsg)
	if err := viper.BindPFlag("endpoint", mountCmd.Flags().Lookup("endpoint")); err != nil {
		panic("INTERNAL ERROR: could not bind endpoint flag to endpoint environment variable")
	}

	mountCmd.Flags().IntVarP(&awsBatch, "aws-batch", "", awsDefault, flags.AwsBatchMsg)
	if err := viper.BindPFlag("aws-batch", mountCmd.Flags().Lookup("aws-batch")); err != nil {
		panic("INTERNAL ERROR: could not bind aw-batch flag to aw-batch environment variable")
	}

	mountCmd.Flags().IntVarP(&gcpBatch, "gcp-batch", "", gcpDefault, flags.GcpBatchMsg)
	if err := viper.BindPFlag("gcp-batch", mountCmd.Flags().Lookup("gcp-batch")); err != nil {
		panic("INTERNAL ERROR: could not bind gcp-batch flag to gcp-batch environment variable")
	}

	rootCmd.AddCommand(mountCmd)
}

var mountCmd = &cobra.Command{
	Use:   "mount [flags] /path/to/mountpoint",
	Short: "Mount a running instance of Fusera to a folder.",
	Args:  cobra.ExactArgs(1),
	RunE:  mount,
}

func mount(cmd *cobra.Command, args []string) (err error) {
	setConfig()
	twig.Debug("got mount command")
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
		return errors.New("No accessions provided: Fusera needs a list of accessions in order to know what files to provide in its file system.")
	}
	// Now resolveAccession's value
	accs, err := flags.ResolveAccession(accession)
	if err != nil {
		return err
	}
	// Validate mount point
	// Do mount stuff

	// Location takes longest if there's a failure, so validate it last.
	if location == "" {
		location, err = flags.ResolveLocation()
		if err != nil {
			twig.Debug(err)
			return errors.New("No location: A location was not provided so Fusera attempted to resolve the location itself. This feature is only supported when Fusera is running on Amazon or Google's cloud platforms.")
		}
	}
	var types map[string]bool
	if filetype != "" {
		types, err = flags.ResolveFileType(filetype)
		if err != nil {
			return errors.New("Seems like something was wrong with the format of the filetype flag.")
		}
	}
	uid, gid := myUserAndGroup()
	opt := &fuseralib.Options{
		Acc:       accs,
		Loc:       location,
		Ngc:       ngc,
		Filetypes: types,

		ApiEndpoint: endpoint,
		AwsBatch:    awsBatch,
		GcpBatch:    gcpBatch,

		DirMode:  0555,
		FileMode: 0444,
		Uid:      uint32(uid),
		Gid:      uint32(gid),
		// TODO: won't need.
		MountOptions:  make(map[string]string),
		MountPoint:    args[0],
		MountPointArg: args[0],
	}
	fs, mfs, err := fuseralib.Mount(context.Background(), opt)
	if err != nil {
		var msg string
		if strings.Contains(err.Error(), "no such file or directory") {
			msg = "Fusera failed to mount the file system.\nIt seems like the directory you want to mount to does not exist or you do not have correct permissions to access it. Please create the directory or correct the permissions on it before trying again."
		}
		if strings.Contains(err.Error(), "EOF") {
			msg = "Fusera failed to mount the file system.\nIt seems like the directory you want to mount to is already mounted by fusera or another device. Choose another directory or try using the unmount command before trying again. Be considerate of the unmount command, if anything is using the device mounted while attempting to unmount, it will fail."
		}
		twig.Debugf("%+v\n", err)
		return errors.New(msg)
	}
	twig.Debug("File system has been successfully mounted.")
	// Let the user unmount with Ctrl-C
	registerSIGINTHandler(fs, opt.MountPoint)

	// Wait for the file system to be unmounted.
	err = mfs.Join(context.Background())
	if err != nil {
		fmt.Println("fusera encountered an internal issue, please rerun with the --debug flag to learn more.")
		twig.Debugf("FATAL: MountedFileSystem.Join: %+#v\n", err)
		os.Exit(1)
	}

	return nil
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

func myUserAndGroup() (int, int) {
	user, err := user.Current()
	if err != nil {
		panic(err)
	}
	uid64, err := strconv.ParseInt(user.Uid, 10, 32)
	if err != nil {
		panic(errors.Wrapf(err, "Parsing UID (%s)", user.Uid))
	}
	gid64, err := strconv.ParseInt(user.Gid, 10, 32)
	if err != nil {
		panic(errors.Wrapf(err, "Parsing GID (%s)", user.Gid))
	}
	return int(uid64), int(gid64)
}

func registerSIGINTHandler(fs *fuseralib.Fusera, mountPoint string) {
	// Register for SIGINT.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)

	// Start a goroutine that will unmount when the signal is received.
	go func() {
		for {
			s := <-signalChan
			if s == syscall.SIGUSR1 {
				twig.Debugf("Received %v", s)
				fs.SigUsr1()
				continue
			}

			twig.Debugf("Received %v, attempting to unmount...", s)

			err := fuseralib.TryUnmount(mountPoint)
			if err != nil {
				twig.Debugf("Failed to unmount in response to %v: %v", s, err)
			} else {
				twig.Debugf("Successfully unmounted %v in response to %v",
					mountPoint, s)
				return
			}
		}
	}()
}
