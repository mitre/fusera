// Copyright 2015 - 2017 Ka-Hing Cheung
// Copyright 2015 - 2017 Google Inc. All Rights Reserved.
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

package internal

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
		Usage:    "",
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
				Usage: "comma separated list of SRR#'s that are to be mounted.",
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

	for _, f := range []string{"help, h", "debug_fuse", "debug_service", "version, v", "f"} {
		flagCategories[f] = "misc"
	}

	cli.HelpPrinter = func(w io.Writer, templ string, data interface{}) {
		w = tabwriter.NewWriter(w, 1, 8, 2, ' ', 0)
		var tmplGet = template.Must(template.New("help").Funcs(funcMap).Parse(templ))
		tmplGet.Execute(w, app)
	}

	return
}

type FlagStorage struct {
	// Fusera flags
	Ngc string
	Acc []string
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

func parseOptions(m map[string]string, s string) {
	// NOTE(jacobsa): The man pages don't define how escaping works, and as far
	// as I can tell there is no way to properly escape or quote a comma in the
	// options list for an fstab entry. So put our fingers in our ears and hope
	// that nobody needs a comma.
	for _, p := range strings.Split(s, ",") {
		var name string
		var value string

		// Split on the first equals sign.
		if equalsIndex := strings.IndexByte(p, '='); equalsIndex != -1 {
			name = p[:equalsIndex]
			value = p[equalsIndex+1:]
		} else {
			name = p
		}

		m[name] = value
	}

	return
}

func (flags *FlagStorage) Cleanup() {
	if flags.MountPointCreated != "" && flags.MountPointCreated != flags.MountPointArg {
		err := os.Remove(flags.MountPointCreated)
		if err != nil {
			log.Errorf("rmdir %v = %v", flags.MountPointCreated, err)
		}
	}
}

// Add the flags accepted by run to the supplied flag set, returning the
// variables into which the flags will parse.
func PopulateFlags(c *cli.Context) (ret *FlagStorage) {
	uid, gid := MyUserAndGroup()
	flags := &FlagStorage{
		// Fusera
		Ngc: c.String("ngc"),
		Acc: strings.Split(c.String("acc"), ","),
		Loc: c.String("loc"),

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

	flags.MountPointArg = c.Args()[0]
	flags.MountPoint = flags.MountPointArg

	twig.SetDebug(flags.Debug)

	var err error

	defer func() {
		if err != nil {
			flags.Cleanup()
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
			return nil
		}

		if _, ok := flags.MountOptions["allow_other"]; !ok {
			flags.MountPointCreated, err = ioutil.TempDir("", ".goofys-mnt")
			if err != nil {
				io.WriteString(cli.ErrWriter,
					fmt.Sprintf("Unable to create temp dir: %v", err))
				return nil
			}
			flags.MountPoint = flags.MountPointCreated
		}

		cacheArgs = append([]string{"--test"}, cacheArgs...)

		if flags.MountPointArg == flags.MountPoint {
			cacheArgs = append(cacheArgs, "-ononempty")
		}

		cacheArgs = append(cacheArgs, "--")
		cacheArgs = append(cacheArgs, flags.MountPoint)
		cacheArgs = append(cacheArgs, cacheDir)
		cacheArgs = append(cacheArgs, flags.MountPointArg)

		fuseLog.Debugf("catfs %v", cacheArgs)
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
			return nil
		}

		flags.Cache = cacheArgs[1:]
	}

	return flags
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
		log.Fatalf("Parsing UID (%s): %v", user.Uid, err)
		return
	}

	// Parse GID.
	gid64, err := strconv.ParseInt(user.Gid, 10, 32)
	if err != nil {
		log.Fatalf("Parsing GID (%s): %v", user.Gid, err)
		return
	}

	uid = int(uid64)
	gid = int(gid64)

	return
}
