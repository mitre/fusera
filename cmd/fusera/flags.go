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
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/log"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var flagCategories map[string]string

// Set up custom help text for goofys; in particular the usage section.
func filterCategory(flags []cli.Flag, category string) (ret []cli.Flag) {
	for _, f := range flags {
		if flagCategories[f.GetName()] == category {
			ret = append(ret, f)
		}
	}
	return
}

func init() {
	cli.AppHelpTemplate = `NAME:
   {{.Name}} - {{.Usage}}

USAGE:
   {{.Name}} {{if .Flags}}[global options]{{end}} mountpoint
   {{if .Version}}
VERSION:
   {{.Version}}
   {{end}}{{if len .Authors}}
AUTHOR(S):
   {{range .Authors}}{{ . }}{{end}}
   {{end}}{{if .Commands}}
COMMANDS:
   {{range .Commands}}{{join .Names ", "}}{{ "\t" }}{{.Usage}}
   {{end}}{{end}}{{if .Flags}}
GLOBAL OPTIONS:
   {{range category .Flags ""}}{{.}}
   {{end}}
MISC OPTIONS:
   {{range category .Flags "misc"}}{{.}}
   {{end}}{{end}}{{if .Copyright }}
COPYRIGHT:
   {{.Copyright}}
   {{end}}
`
}

var VersionHash string

func NewApp() (app *cli.App) {

	app = &cli.App{
		Name:     "fusera",
		Version:  "0.0.-" + VersionHash,
		Usage:    "A FUSE interface to the NCBI Sequence Read Archive (SRA)",
		HideHelp: true,
		Writer:   os.Stderr,
		Flags: []cli.Flag{

			cli.BoolFlag{
				Name:  "help, h",
				Usage: "Print this help text and exit successfully.",
			},

			/////////////////////////
			// Fusera
			/////////////////////////

			cli.StringFlag{
				Name:  "ngc",
				Usage: "path to an ngc file that contains authentication info.",
			},
			cli.StringFlag{
				Name:  "acc",
				Usage: "comma separated list of SRR#s that are to be mounted.",
			},
			cli.StringFlag{
				Name:  "acc-file",
				Usage: "path to file with comma or space separated list of SRR#s that are to be mounted.",
			},
			cli.StringFlag{
				Name:  "loc",
				Usage: "preferred region.",
			},

			/////////////////////////
			// File system
			/////////////////////////

			// cli.StringFlag{
			// 	Name: "cache",
			// 	Usage: "Directory to use for data cache. " +
			// 		"Requires catfs and `-o allow_other'. " +
			// 		"Can also pass in other catfs options " +
			// 		"(ex: --cache \"--free:10%:$HOME/cache\") (default: off)",
			// },

			/////////////////////////
			// Debugging
			/////////////////////////
			cli.BoolFlag{
				Name:  "debug",
				Usage: "Enable debugging output.",
			},
			cli.BoolFlag{
				Name:  "debug_fuse",
				Usage: "Enable fuse-related debugging output.",
			},
			cli.BoolFlag{
				Name:  "debug_service",
				Usage: "Enable service-related debugging output.",
			},
			cli.BoolFlag{
				Name:  "f",
				Usage: "Run fusera in foreground.",
			},
		},
	}

	var funcMap = template.FuncMap{
		"category": filterCategory,
		"join":     strings.Join,
	}

	flagCategories = map[string]string{}

	for _, f := range []string{"help, h", "debug", "debug_fuse", "debug_service", "version, v", "f"} {
		flagCategories[f] = "misc"
	}

	cli.HelpPrinter = func(w io.Writer, templ string, data interface{}) {
		w = tabwriter.NewWriter(w, 1, 8, 2, ' ', 0)
		var tmplGet = template.Must(template.New("help").Funcs(funcMap).Parse(templ))
		tmplGet.Execute(w, app)
	}

	return
}

type Flags struct {
	// Fusera flags
	Ngc []byte
	Acc map[string]bool
	Loc string
	// SRR# has a map of file names that map to urls where the data is
	Urls map[string]map[string]string

	// File system
	MountOptions      map[string]string
	MountPoint        string
	MountPointArg     string
	MountPointCreated string

	Cache    []string
	DirMode  os.FileMode
	FileMode os.FileMode
	Uid      uint32
	Gid      uint32

	// Tuning
	StatCacheTTL time.Duration
	TypeCacheTTL time.Duration

	// Debugging
	Debug      bool
	DebugFuse  bool
	DebugS3    bool
	Foreground bool
}

func (f *Flags) Cleanup() {
	if f.MountPointCreated != "" && f.MountPointCreated != f.MountPointArg {
		err := os.Remove(f.MountPointCreated)
		if err != nil {
			mainLog.Errorf("rmdir %v = %v", f.MountPointCreated, err)
		}
	}
}

// Add the flags accepted by run to the supplied flag set, returning the
// variables into which the flags will parse.
func PopulateFlags(c *cli.Context) (ret *Flags, err error) {
	uid, gid := MyUserAndGroup()
	f := &Flags{
		Acc: make(map[string]bool),
		// File system
		MountOptions: make(map[string]string),
		DirMode:      0755,
		FileMode:     0644,
		Uid:          uint32(uid),
		Gid:          uint32(gid),

		// Tuning,
		StatCacheTTL: time.Hour * 24 * 365 * 7,
		TypeCacheTTL: time.Hour * 24 * 365 * 7,

		// Debugging,
		Debug:      c.Bool("debug"),
		DebugFuse:  c.Bool("debug_fuse"),
		DebugS3:    c.Bool("debug_s3"),
		Foreground: c.Bool("f"),
	}
	ngcpath := c.String("ngc")
	if ngcpath != "" {
		// we were given a path to an ngc file. Let's read it.
		data, err := ioutil.ReadFile(ngcpath)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open ngc file at: %s", ngcpath)
		}
		f.Ngc = data
	}
	aa := strings.Split(c.String("acc"), ",")
	if len(aa) == 1 && aa[0] == "" {
		aa = nil
	}
	if len(aa) > 0 {
		// append SRRs to actual acc list.
		for _, a := range aa {
			if a != "" {
				f.Acc[a] = true
			}
		}
	}
	accpath := c.String("acc-file")
	if accpath != "" {
		// we were given a path to an acc file. Let's read it and append accs to actual acc list.
		data, err := ioutil.ReadFile(accpath)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open acc file at: %s", accpath)
		}
		accs := reconcileAccs(data)
		for _, a := range accs {
			if a != "" {
				f.Acc[a] = true
			}
		}
	}
	if len(aa) == 0 && accpath == "" {
		return nil, errors.New("must provide at least one accession number")
	}
	// parseLocation()
	loc := c.String("loc")
	// loc = strings.ToLower(loc)
	if loc == "" {
		return nil, errors.New("must provide a location of either s3.us-east-1 or gs.US")
	}
	ll := strings.Split(loc, ".")
	if len(ll) != 2 {
		return nil, errors.New("location must be either gs.US or s3.us-east-1")
	}
	if ll[0] != "gs" && ll[0] != "s3" {
		return nil, errors.Errorf("the service %s is not supported, please use gs or s3", ll[0])
	}
	if ll[0] == "gs" {
		if ll[1] != "US" {
			return nil, errors.Errorf("the region %s isn't supported on gs, only US", ll[1])
		}
	}
	if ll[0] == "s3" {
		if ll[1] != "us-east-1" {
			return nil, errors.Errorf("the region %s isn't supported on s3, only us-east-1", ll[1])
		}
	}
	f.Loc = loc

	f.MountPointArg = c.Args()[0]
	f.MountPoint = f.MountPointArg

	twig.SetDebug(f.Debug)

	defer func() {
		if err != nil {
			f.Cleanup()
		}
	}()

	if c.IsSet("cache") {
		cache := c.String("cache")
		cacheArgs := strings.Split(c.String("cache"), ":")
		cacheDir := cacheArgs[len(cacheArgs)-1]
		cacheArgs = cacheArgs[:len(cacheArgs)-1]

		fi, err := os.Stat(cacheDir)
		if err != nil || !fi.IsDir() {
			io.WriteString(cli.ErrWriter,
				fmt.Sprintf("Invalid value \"%v\" for --cache: not a directory\n\n",
					cacheDir))
			return nil, nil
		}

		if _, ok := f.MountOptions["allow_other"]; !ok {
			f.MountPointCreated, err = ioutil.TempDir("", ".goofys-mnt")
			if err != nil {
				io.WriteString(cli.ErrWriter,
					fmt.Sprintf("Unable to create temp dir: %v", err))
				return nil, nil
			}
			f.MountPoint = f.MountPointCreated
		}

		cacheArgs = append([]string{"--test"}, cacheArgs...)

		if f.MountPointArg == f.MountPoint {
			cacheArgs = append(cacheArgs, "-ononempty")
		}

		cacheArgs = append(cacheArgs, "--")
		cacheArgs = append(cacheArgs, f.MountPoint)
		cacheArgs = append(cacheArgs, cacheDir)
		cacheArgs = append(cacheArgs, f.MountPointArg)

		log.FuseLog.Debugf("catfs %v", cacheArgs)
		catfs := exec.Command("catfs", cacheArgs...)
		_, err = catfs.Output()
		if err != nil {
			if ee, ok := err.(*exec.Error); ok {
				io.WriteString(cli.ErrWriter,
					fmt.Sprintf("--cache requires catfs (%v) but %v\n\n",
						"http://github.com/kahing/catfs",
						ee.Error()))
			} else if ee, ok := err.(*exec.ExitError); ok {
				io.WriteString(cli.ErrWriter,
					fmt.Sprintf("Invalid value \"%v\" for --cache: %v\n\n",
						cache, string(ee.Stderr)))
			}
			return nil, nil
		}

		f.Cache = cacheArgs[1:]
	}

	return f, nil
}

func MassageMountFlags(args []string) (ret []string) {
	if len(args) == 5 && args[3] == "-o" {
		// looks like it's coming from fstab!
		mountOptions := ""
		ret = append(ret, args[0])

		for _, p := range strings.Split(args[4], ",") {
			if strings.HasPrefix(p, "-") {
				ret = append(ret, p)
			} else {
				mountOptions += p
				mountOptions += ","
			}
		}

		if len(mountOptions) != 0 {
			// remove trailing ,
			mountOptions = mountOptions[:len(mountOptions)-1]
			ret = append(ret, "-o")
			ret = append(ret, mountOptions)
		}

		ret = append(ret, args[1])
		ret = append(ret, args[2])
	} else {
		return args
	}

	return
}

// Return the UID and GID of this process.
func MyUserAndGroup() (uid int, gid int) {
	// Ask for the current user.
	user, err := user.Current()
	if err != nil {
		panic(err)
	}

	// Parse UID.
	uid64, err := strconv.ParseInt(user.Uid, 10, 32)
	if err != nil {
		mainLog.Fatalf("Parsing UID (%s): %v", user.Uid, err)
		return
	}

	// Parse GID.
	gid64, err := strconv.ParseInt(user.Gid, 10, 32)
	if err != nil {
		mainLog.Fatalf("Parsing GID (%s): %v", user.Gid, err)
		return
	}

	uid = int(uid64)
	gid = int(gid64)

	return
}

func reconcileAccs(data []byte) []string {
	accs := strings.Split(string(data), ",")
	if len(accs) != 1 {
		return accs
	}
	accs = strings.Split(string(data), " ")
	if len(accs) != 1 {
		return accs
	}
	accs = strings.Split(string(data), "\n")
	return vetAccs(accs)
}

func vetAccs(accs []string) []string {
	aa := make([]string, 0, len(accs))
	for _, a := range(accs) {
		if !strings.Contains(a, "SRR") ||
			strings.Contains(a, " ") ||
			strings.Contains(a, ",") ||
			strings.Contains(a, "\n") {
			continue
		}
		aa = append(aa, a)
	}
	return aa
}
