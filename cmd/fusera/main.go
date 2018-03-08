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
	"time"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera"
	"github.com/mitre/fusera/log"
	"github.com/pkg/errors"

	"github.com/jacobsa/fuse"
	"github.com/kardianos/osext"
	"github.com/urfave/cli"

	daemon "github.com/sevlyar/go-daemon"
)

func init() {
	twig.SetFlags(twig.LstdFlags | twig.Lshortfile)
}

var mainLog = log.GetLogger("main")

func registerSIGINTHandler(fs *fusera.Fusera, flags *Flags) {
	// Register for SIGINT.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)

	// Start a goroutine that will unmount when the signal is received.
	go func() {
		for {
			s := <-signalChan
			if s == syscall.SIGUSR1 {
				mainLog.Infof("Received %v", s)
				fs.SigUsr1()
				continue
			}

			if len(flags.Cache) == 0 {
				mainLog.Infof("Received %v, attempting to unmount...", s)

				err := fusera.TryUnmount(flags.MountPoint)
				if err != nil {
					mainLog.Errorf("Failed to unmount in response to %v: %v", s, err)
				} else {
					mainLog.Printf("Successfully unmounted %v in response to %v",
						flags.MountPoint, s)
					return
				}
			} else {
				mainLog.Infof("Received %v", s)
				// wait for catfs to die and cleanup
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

func kill(pid int, s os.Signal) (err error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	defer p.Release()

	err = p.Signal(s)
	if err != nil {
		return err
	}
	return
}

// Mount the file system based on the supplied arguments, returning a
// fuse.MountedFileSystem that can be joined to wait for unmounting.
func mount(ctx context.Context, flags *Flags) (*fusera.Fusera, *fuse.MountedFileSystem, error) {
	// TODO: change flags to fusera options
	opt := &fusera.Options{
		Acc:               flags.Acc,
		Ngc:               flags.Ngc,
		Loc:               flags.Loc,
		Urls:              flags.Urls,
		MountOptions:      flags.MountOptions,
		MountPoint:        flags.MountPoint,
		MountPointArg:     flags.MountPointArg,
		MountPointCreated: flags.MountPointCreated,
		Cache:             flags.Cache,
		DirMode:           flags.DirMode,
		FileMode:          flags.FileMode,
		Uid:               flags.Uid,
		Gid:               flags.Gid,
		StatCacheTTL:      flags.StatCacheTTL,
		TypeCacheTTL:      flags.TypeCacheTTL,
		Debug:             flags.Debug,
		DebugFuse:         flags.DebugFuse,
		DebugS3:           flags.DebugS3,
		Foreground:        flags.Foreground,
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

	app := NewApp()

	var flags *Flags
	var child *os.Process

	app.Action = func(c *cli.Context) (err error) {
		// We should get one argument exactly. Otherwise error out.
		if len(c.Args()) != 1 {
			fmt.Printf("\n%s takes exactly one argument.\n\n", app.Name)
			cli.ShowAppHelp(c)
			os.Exit(1)
		}

		// Populate and parse flags.
		flags, err = PopulateFlags(c)
		if err != nil {
			cause := errors.Cause(err)
			if os.IsPermission(cause) {
				fmt.Print("\nSeems like fusera doesn't have permissions to read a file!")
				fmt.Printf("\nTry changing the permissions with chmod +r path/to/file\n")
			}
			fmt.Printf("\ninvalid arguments: %s\n\n", errors.Cause(err))
			twig.Debugf("%+#v", err.Error())
			cli.ShowAppHelp(c)
			return
		}
		defer func() {
			time.Sleep(time.Second)
			flags.Cleanup()
		}()

		// // Evaluate mandatory flags
		// if flags.Acc == nil {
		// 	twig.Info("fusera expects a list of accessions\n\n")
		// 	os.Exit(1)
		// }
		twig.Debugf("accs: %s", flags.Acc)

		if !flags.Foreground {
			var wg sync.WaitGroup
			waitForSignal(&wg)

			massageArg0()

			ctx := new(daemon.Context)
			child, err = ctx.Reborn()

			if err != nil {
				twig.Info(fmt.Sprintf("FATAL: unable to daemonize: %v\n", err))
				twig.Debug(fmt.Sprintf("FATAL: unable to daemonize: %+v\n", err))
				os.Exit(1)
			}

			log.InitLoggers(!flags.Foreground && child == nil)

			if child != nil {
				// attempt to wait for child to notify parent
				wg.Wait()
				if waitedForSignal == syscall.SIGUSR1 {
					return
				} else {
					return fuse.EINVAL
				}
			} else {
				// kill our own waiting goroutine
				kill(os.Getpid(), syscall.SIGUSR1)
				wg.Wait()
				defer ctx.Release()
			}

		} else {
			log.InitLoggers(!flags.Foreground)
		}

		// Mount the file system.
		var mfs *fuse.MountedFileSystem
		var fs *fusera.Fusera
		fs, mfs, err = mount(context.Background(), flags)
		if err != nil {
			if !flags.Foreground {
				kill(os.Getppid(), syscall.SIGUSR2)
			}
			twig.Info(fmt.Sprintf("FATAL: Mounting file system: %v\n", err))
			twig.Debug(fmt.Sprintf("FATAL: Mounting file system: %+v\n", err))
			os.Exit(1)
		} else {
			if !flags.Foreground {
				kill(os.Getppid(), syscall.SIGUSR1)
			}
			twig.Info("File system has been successfully mounted.")
			// Let the user unmount with Ctrl-C
			// (SIGINT). But if cache is on, catfs will
			// receive the signal and we would detect that exiting
			registerSIGINTHandler(fs, flags)

			// Wait for the file system to be unmounted.
			err = mfs.Join(context.Background())
			if err != nil {
				twig.Info(fmt.Sprintf("FATAL: MountedFileSystem.Join: %v", err))
				twig.Debug(fmt.Sprintf("FATAL: MountedFileSystem.Join: %+v", err))
				return
			}

			twig.Info("Successfully exiting.")
		}
		return
	}

	err := app.Run(MassageMountFlags(os.Args))
	if err != nil {
		if flags != nil && !flags.Foreground && child != nil {
			// fmt.Fprint(os.Stderr, "Unable to mount file system, see syslog for details")
			twig.Info(fmt.Sprintf("FATAL: Unable to mount file system: %v", err))
			twig.Debug(fmt.Sprintf("FATAL: Unable to mount file system: %+v", err))
		}
		os.Exit(1)
	}
}
