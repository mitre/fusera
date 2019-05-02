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
	"github.com/mitre/fusera/awsutil"
	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/fuseralib"
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

	mountCmd.Flags().StringVarP(&flags.Endpoint, "endpoint", "e", "https://www.ncbi.nlm.nih.gov/Traces/sdl/1/retrieve", flags.EndpointMsg)
	if err := viper.BindPFlag("endpoint", mountCmd.Flags().Lookup("endpoint")); err != nil {
		panic("INTERNAL ERROR: could not bind endpoint flag to endpoint environment variable")
	}

	mountCmd.Flags().IntVarP(&flags.AwsBatch, "aws-batch", "", flags.AwsDefault, flags.AwsBatchMsg)
	if err := viper.BindPFlag("aws-batch", mountCmd.Flags().Lookup("aws-batch")); err != nil {
		panic("INTERNAL ERROR: could not bind aws-batch flag to aws-batch environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.AwsProfile, "aws-profile", "", flags.AwsProfileDefault, flags.AwsProfileMsg)
	if err := viper.BindPFlag("aws-profile", mountCmd.Flags().Lookup("aws-profile")); err != nil {
		panic("INTERNAL ERROR: could not bind aws-profile flag to aws-profile environment variable")
	}

	mountCmd.Flags().IntVarP(&flags.GcpBatch, "gcp-batch", "", flags.GcpDefault, flags.GcpBatchMsg)
	if err := viper.BindPFlag("gcp-batch", mountCmd.Flags().Lookup("gcp-batch")); err != nil {
		panic("INTERNAL ERROR: could not bind gcp-batch flag to gcp-batch environment variable")
	}

	mountCmd.Flags().StringVarP(&flags.GcpProfile, "gcp-profile", "", flags.GcpProfileDefault, flags.GcpProfileMsg)
	if err := viper.BindPFlag("gcp-profile", mountCmd.Flags().Lookup("gcp-profile")); err != nil {
		panic("INTERNAL ERROR: could not bind gcp-profile flag to gcp-profile environment variable")
	}

	mountCmd.Flags().BoolVarP(&flags.Eager, "eager", "", false, "ADVANCED: Have fusera request that urls be signed by the API on start up.\nEnvironment Variable: [$DBGAP_EAGER]")
	if err := viper.BindPFlag("eager", mountCmd.Flags().Lookup("eager")); err != nil {
		panic("INTERNAL ERROR: could not bind eager flag to eager environment variable")
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
	tokenpath := foldNgcIntoToken(flags.Tokenpath, flags.NgcPath)
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
	if strings.HasPrefix(flags.Location, "gs") {
		return errors.New("the manual setting of a google cloud location is not permitted, please allow fusera to resolve the location itself")
	}
	// Location takes longest if there's a failure, so validate it last.
	var platform *awsutil.Platform
	if flags.Location == "" {
		platform, err = flags.FindLocation()
		if err != nil {
			twig.Debug(err)
			fmt.Println(err)
			return errors.New("no location provided")
		}
	} else {
		platform, err = awsutil.NewManualPlatform(flags.Location)
		if err != nil {
			twig.Debug(err)
			fmt.Println(err)
			return err
		}
	}

	uid, gid := myUserAndGroup()
	batch := flags.ResolveBatch(platform.Name, flags.AwsBatch, flags.GcpBatch)

	var accessions []*fuseralib.Accession
	var client fuseralib.API
	var rootErr []byte
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
	if flags.Eager {
		client = sdl.NewEagerClient(flags.Endpoint, location, token, types)
	} else {
		client = sdl.NewClient(flags.Endpoint, location, token, types)
	}
	if flags.Verbose {
		fmt.Printf("Communicating with SDL API at: %s\n", flags.Endpoint)
		fmt.Printf("Using token at: %s\n", flags.Tokenpath)
		fmt.Printf("Contents of token: %s\n", string(token[:]))
		fmt.Printf("Limiting file types to: %v\n", types)
		fmt.Printf("Giving location as: %s\n", string(platform.Region[:]))
		fmt.Printf("Requesting accessions in batches of: %d\n", batch)
	}
	if accs == nil || len(accs) == 0 { // We have no accessions
		aa, err := client.Retrieve(nil)
		if err != nil {
			rootErr = append(rootErr, []byte(fmt.Sprintln(err.Error()))...)
			if !flags.Silent {
				fmt.Println(string(rootErr))
			}
		} else {
			accessions = append(accessions, aa...)
		}
	} else { // We have accessions and we need to respect batch sizes.
		dot := batch
		i := 0
		for dot < len(accs) {
			aa, err := client.Retrieve(accs[i:dot])
			if err != nil {
				rootErr = append(rootErr, []byte(fmt.Sprintln(err.Error()))...)
				rootErr = append(rootErr, []byte("List of accessions that failed in this batch:\n")...)
				rootErr = append(rootErr, []byte(fmt.Sprintln(accs[i:dot]))...)
				if !flags.Silent {
					fmt.Println(string(rootErr))
				}
			} else {
				accessions = append(accessions, aa...)
			}
			i = dot
			dot += batch
		}
		aa, err := client.Retrieve(accs[i:])
		if err != nil {
			rootErr = append(rootErr, []byte(fmt.Sprintln(err.Error()))...)
			rootErr = append(rootErr, []byte("List of accessions that failed in this batch:\n")...)
			rootErr = append(rootErr, []byte(fmt.Sprintln(accs[i:]))...)
			if !flags.Silent {
				fmt.Println(string(rootErr))
			}
		} else {
			accessions = append(accessions, aa...)
		}
	}
	if len(accessions) == 0 {
		if !flags.Silent {
			fmt.Println("It seems like none of the accessions were successful, fusera is shutting down.")
		}
		os.Exit(1)
	}
	credProfile := ""
	if platform.IsAWS() {
		credProfile = flags.AwsProfile
	}
	if platform.IsGCP() {
		credProfile = flags.GcpProfile
		client = sdl.NewGCPClient(flags.Endpoint, token, types)
	}
	opt := &fuseralib.Options{
		API:      client,
		Acc:      accessions,
		Platform: platform,
		Profile:  credProfile,

		UID:           uint32(uid),
		GID:           uint32(gid),
		MountOptions:  make(map[string]string),
		MountPoint:    mountpoint,
		MountPointArg: mountpoint,
	}

	if flags.Verbose {
		fmt.Printf("Profile for credentials if needed: %s\n", credProfile)
		fmt.Printf("Platform: %s\n", opt.Platform.Name)
		fmt.Printf("Region: %s\n", string(opt.Platform.Region))
		fmt.Printf("Mountpoint: %s\n", opt.MountPoint)
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

func foldEnvVarsIntoFlagValues() {
	flags.ResolveString("endpoint", &flags.Endpoint)
	flags.ResolveInt("aws-batch", &flags.AwsBatch)
	flags.ResolveInt("gcp-batch", &flags.GcpBatch)
	flags.ResolveString("aws-profile", &flags.AwsProfile)
	flags.ResolveString("gcp-profile", &flags.GcpProfile)
	flags.ResolveBool("eager", &flags.Eager)
	flags.ResolveString("location", &flags.Location)
	flags.ResolveString("accession", &flags.Accession)
	flags.ResolveString("token", &flags.Tokenpath)
	flags.ResolveString("ngc", &flags.NgcPath)
	flags.ResolveString("filetype", &flags.Filetype)
}

func foldNgcIntoToken(token, ngc string) string {
	if ngc != "" && token == "" {
		return ngc
	}
	return token
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
