// Copyright 2015 - 2017 Ka-Hing Cheung
// Copyright 2015 - 2017 Google Inc. All Rights Reserved.
// Modifications Copyright 2018 The MITRE Corporation
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

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera"

	"github.com/jacobsa/fuse"
	"github.com/kardianos/osext"
)

func init() {
	twig.SetFlags(twig.LstdFlags | twig.Lshortfile)
}

func registerSIGINTHandler(fs *fusera.Fusera, flags *Flags) {
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

			err := fusera.TryUnmount(flags.MountPoint)
			if err != nil {
				twig.Debugf("Failed to unmount in response to %v: %v", s, err)
			} else {
				twig.Debugf("Successfully unmounted %v in response to %v",
					flags.MountPoint, s)
				return
			}
		}
	}()
}

var waitedForSignal os.Signal

func waitForSignal(wg *sync.WaitGroup) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGUSR1, syscall.SIGUSR2)

	wg.Add(1)
	go func() {
		waitedForSignal = <-signalChan
		wg.Done()
	}()
}

// Mount the file system based on the supplied arguments, returning a
// fuse.MountedFileSystem that can be joined to wait for unmounting.
func mount(ctx context.Context, flags *Flags) (*fusera.Fusera, *fuse.MountedFileSystem, error) {
	opt := &fusera.Options{
		Acc:               flags.Acc,
		Ngc:               flags.Ngc,
		Loc:               flags.Loc,
		ApiEndpoint:       flags.Endpoint,
		AwsBatch:          flags.AwsBatch,
		GcpBatch:          flags.GcpBatch,
		MountOptions:      flags.MountOptions,
		MountPoint:        flags.MountPoint,
		MountPointArg:     flags.MountPointArg,
		MountPointCreated: flags.MountPointCreated,
		DirMode:           flags.DirMode,
		FileMode:          flags.FileMode,
		Uid:               flags.Uid,
		Gid:               flags.Gid,
		Debug:             flags.Debug,
	}
	return fusera.Mount(ctx, opt)
}

func massagePath() {
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "PATH=") {
			return
		}
	}

	// mount -a seems to run goofys without PATH
	// usually fusermount is in /bin
	os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
}

func massageArg0() {
	var err error
	os.Args[0], err = osext.Executable()
	if err != nil {
		panic(fmt.Sprintf("Unable to discover current executable: %v", err))
	}
}

var Version = "beta"

func main() {
	VersionHash = Version
	massagePath()
	app, cmd := NewApp()
	err := app.Run(MassageMountFlags(os.Args))
	if err != nil {
		fmt.Println("starting fusera with given arguments failed, please review the help with -h")
		twig.Debugf("%+v\n", err)
		os.Exit(1)
	}
	if cmd.IsMount {
		// Mount the file system.
		var mfs *fuse.MountedFileSystem
		var fs *fusera.Fusera
		fs, mfs, err = mount(context.Background(), cmd.Flags)
		if err != nil {
			fmt.Println("Fusera failed to mount the file system")
			if strings.Contains(err.Error(), "no such file or directory") {
				fmt.Println("It seems like the directory you want to mount to does not exist or you do not have correct permissions to access it. Please create the directory or correct the permissions on it before trying again.")
			}
			if strings.Contains(err.Error(), "EOF") {
				fmt.Println("It seems like the directory you want to mount to is already mounted by fusera or another device. Choose another directory or try using the unmount command before trying again. Be considerate of the unmount command, if anything is using the device mounted while attempting to unmount, it will fail.")
			}
			fmt.Println("Details: " + err.Error())
			twig.Debugf("%+v\n", err)
			os.Exit(1)
		}
		twig.Debug("File system has been successfully mounted.")
		// Let the user unmount with Ctrl-C
		registerSIGINTHandler(fs, cmd.Flags)

		// Wait for the file system to be unmounted.
		err = mfs.Join(context.Background())
		if err != nil {
			fmt.Println("fusera encountered an internal issue, please rerun with the --debug flag to learn more.")
			twig.Debugf("FATAL: MountedFileSystem.Join: %+#v\n", err)
			os.Exit(1)
		}
	}
	if cmd.IsUnmount {
		err := fusera.TryUnmount(cmd.Path)
		if err != nil {
			fmt.Printf("Failed to unmount %s\n", cmd.Path)
			twig.Debugf("%+#v\n", err.Error())
			os.Exit(1)
		}
		twig.Debugf("Successfully unmounted %s", cmd.Path)
	}

	twig.Debug("Successfully exiting.")
}
