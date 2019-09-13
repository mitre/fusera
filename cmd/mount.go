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

	"github.com/mitre/fusera/info"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/fuseralib"
	"github.com/mitre/fusera/gps"
	"github.com/mitre/fusera/sdl"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	mountCmd.Flags().StringVarP(&flags.Location, "location", "l", "", flags.LocationMsg)
	if err := viper.BindPFlag("location", mountCmd.Flags().Lookup("location")); err != nil {
		panic("INTERNAL ERROR: could not bind location flag to location environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.Accession, "accession", "a", "", flags.AccessionMsg)
	if err := viper.BindPFlag("accession", mountCmd.Flags().Lookup("accession")); err != nil {
		panic("INTERNAL ERROR: could not bind accession flag to accession environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.Tokenpath, "token", "t", "", flags.TokenMsg)
	if err := viper.BindPFlag("token", mountCmd.Flags().Lookup("token")); err != nil {
		panic("INTERNAL ERROR: could not bind token flag to token environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.NgcPath, "ngc", "n", "", flags.NgcMsg)
	if err := viper.BindPFlag("ngc", mountCmd.Flags().Lookup("ngc")); err != nil {
		panic("INTERNAL ERROR: could not bind ngc flag to ngc environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.Filetype, "filetype", "f", "", flags.FiletypeMsg)
	if err := viper.BindPFlag("filetype", mountCmd.Flags().Lookup("filetype")); err != nil {
		panic("INTERNAL ERROR: could not bind filetype flag to filetype environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.Endpoint, "endpoint", "e", "https://www.ncbi.nlm.nih.gov/Traces/sdl/2/retrieve", flags.EndpointMsg)
	if err := viper.BindPFlag("endpoint", mountCmd.Flags().Lookup("endpoint")); err != nil {
		panic("INTERNAL ERROR: could not bind endpoint flag to endpoint environment variable")
	}

	mountCmd.Flags().IntVarP(&flags.Batch, "batch", "", flags.BatchDefault, flags.BatchMsg)
	if err := viper.BindPFlag("batch", mountCmd.Flags().Lookup("batch")); err != nil {
		panic("INTERNAL ERROR: could not bind batch flag to batch environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.AwsProfile, "aws-profile", "", "", flags.AwsProfileMsg)
	if err := viper.BindPFlag("aws-profile", mountCmd.Flags().Lookup("aws-profile")); err != nil {
		panic("INTERNAL ERROR: could not bind aws-profile flag to aws-profile environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.GcpProfile, "gcp-profile", "", "", flags.GcpProfileMsg)
	if err := viper.BindPFlag("gcp-profile", mountCmd.Flags().Lookup("gcp-profile")); err != nil {
		panic("INTERNAL ERROR: could not bind gcp-profile flag to gcp-profile environment variable")
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
		fmt.Printf("Using token at: %s\n", flags.Tokenpath)
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

	region, err := locator.Region()
	if err != nil {
		if !flags.Silent {
			fmt.Println("It seems like fusera is encountering errors resolving its region, shutting down.")
		}
		os.Exit(1)
	}

	if flags.Verbose {
		fmt.Println("Setting fusera options with:")
		fmt.Printf("Cloud is: %s\n", locator.SdlCloudName())
		fmt.Printf("Region is: %s\n", region)
		fmt.Printf("AWS profile for credentials if needed: %s\n", flags.AwsProfile)
		fmt.Printf("GCP profile for credentials if needed: %s\n", flags.GcpProfile)
		fmt.Printf("Mountpoint: %s\n", mountpoint)
	}
	uid, gid := myUserAndGroup()
	opt := &fuseralib.Options{
		API:           API,
		Acc:           accessions,
		Region:        region,
		CloudProfile:  flags.SetProfile(locator.SdlCloudName()),
		UID:           uint32(uid),
		GID:           uint32(gid),
		MountOptions:  make(map[string]string),
		MountPoint:    mountpoint,
		MountPointArg: mountpoint,
	}

	if !flags.Silent {
		fmt.Println("Fusera is ready!")
		fmt.Println("Remember, Fusera needs to keep running in order to serve your files, don't close this terminal!")
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
