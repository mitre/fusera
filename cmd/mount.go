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
	"syscall"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/fuseralib"
	"github.com/mitre/fusera/sdl"
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
	eager                bool
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
		panic("INTERNAL ERROR: could not bind aws-batch flag to aws-batch environment variable")
	}

	mountCmd.Flags().IntVarP(&gcpBatch, "gcp-batch", "", gcpDefault, flags.GcpBatchMsg)
	if err := viper.BindPFlag("gcp-batch", mountCmd.Flags().Lookup("gcp-batch")); err != nil {
		panic("INTERNAL ERROR: could not bind gcp-batch flag to gcp-batch environment variable")
	}

	mountCmd.Flags().BoolVarP(&eager, "eager", "", false, "ADVANCED: Have fusera request that urls be signed by the API on start up.\nEnvironment Variable: [$DBGAP_EAGER]")
	if err := viper.BindPFlag("eager", mountCmd.Flags().Lookup("eager")); err != nil {
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

// mount locates the files for each accession its given with the SDL API
// and then mounts a FUSE system.
func mount(cmd *cobra.Command, args []string) (err error) {
	setConfig()
	foldEnvVarsIntoFlagValues()
	var ngc []byte
	if ngcpath != "" {
		ngc, err = flags.ResolveNgcFile(ngcpath)
		if err != nil {
			return err
		}
	}
	if accession == "" {
		return errors.New("no accessions provided")
	}
	accs, err := flags.ResolveAccession(accession)
	if err != nil {
		return err
	}
	var types map[string]bool
	if filetype != "" {
		types, err = flags.ResolveFileType(filetype)
		if err != nil {
			return err
		}
	}
	// Validate the mount point before trying to mount to it.
	// So it must exist
	mountpoint := args[0]
	if !flags.FileExists(mountpoint) {
		return errors.New("mountpoint doesn't exist")
	}
	// So it must be readable
	if !flags.HavePermissions(mountpoint) {
		return errors.New("incorrect permissions for mountpoint")
	}
	// Location takes longest if there's a failure, so validate it last.
	if location == "" {
		location, err = flags.ResolveLocation()
		if err != nil {
			twig.Debug(err)
			return errors.New("no location provided")
		}
	}

	uid, gid := myUserAndGroup()
	batch := flags.ResolveBatch(location, awsBatch, gcpBatch)

	client := sdl.NewClient(endpoint, location, ngc, types)
	var accessions []*fuseralib.Accession
	var rootErr []byte
	if !eager {
		dot := 2000
		i := 0
		for dot < len(accs) {
			aa, err := client.GetMetadata(accs[i:dot])
			if err != nil {
				rootErr = append(rootErr, []byte(err.Error())...)
				rootErr = append(rootErr, []byte("List of accessions that failed in this batch:\n")...)
				rootErr = append(rootErr, []byte(fmt.Sprintln(accs[i:dot]))...)
				fmt.Println(rootErr)
			} else {
				accessions = append(accessions, aa...)
			}
			i = dot
			dot += batch
		}
		aa, err := client.GetMetadata(accs[i:])
		if err != nil {
			rootErr = append(rootErr, []byte(err.Error())...)
			rootErr = append(rootErr, []byte("List of accessions that failed in this batch:\n")...)
			rootErr = append(rootErr, []byte(fmt.Sprintln(accs[i:]))...)
			fmt.Println(rootErr)
		} else {
			accessions = append(accessions, aa...)
		}
	} else {
		dot := batch
		i := 0
		for dot < len(accs) {
			aa, err := client.GetSignedURL(accs[i:dot])
			if err != nil {
				rootErr = append(rootErr, []byte(err.Error())...)
				rootErr = append(rootErr, []byte("List of accessions that failed in this batch:\n")...)
				rootErr = append(rootErr, []byte(fmt.Sprintln(accs[i:dot]))...)
				fmt.Println(rootErr)
			} else {
				accessions = append(accessions, aa...)
			}
			i = dot
			dot += batch
		}
		aa, err := client.GetSignedURL(accs[i:])
		if err != nil {
			rootErr = append(rootErr, []byte(err.Error())...)
			rootErr = append(rootErr, []byte("List of accessions that failed in this batch:\n")...)
			rootErr = append(rootErr, []byte(fmt.Sprintln(accs[i:]))...)
			fmt.Println(rootErr)
		} else {
			accessions = append(accessions, aa...)
		}
	}
	//
	opt := &fuseralib.Options{
		Signer: client,
		Acc:    accessions,

		UID: uint32(uid),
		GID: uint32(gid),
		// TODO: won't need.
		MountOptions:  make(map[string]string),
		MountPoint:    mountpoint,
		MountPointArg: mountpoint,
	}
	fs, mfs, err := fuseralib.Mount(context.Background(), opt)
	if err != nil {
		return err
	}
	// Let the user unmount with Ctrl-C
	registerSIGINTHandler(fs, opt.MountPoint)

	// Wait for the file system to be unmounted.
	err = mfs.Join(context.Background())
	if err != nil {
		return errors.Wrap(err, "FATAL")
	}

	return nil
}

func foldEnvVarsIntoFlagValues() {
	flags.ResolveString("endpoint", &endpoint)
	flags.ResolveInt("aws-batch", &awsBatch)
	flags.ResolveInt("gcp-batch", &gcpBatch)
	flags.ResolveBool("eager", &eager)
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
