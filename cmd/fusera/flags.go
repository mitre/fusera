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
	"os/user"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/awsutil"
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
   {{.Name}} <command> [<flags>] mountpoint
   {{if .Version}}
VERSION:
   {{.Version}}
   {{end}}{{if len .Authors}}
AUTHOR(S):
   {{range .Authors}}{{ . }}{{end}}
   {{end}}{{if .Commands}}
COMMANDS:
{{range .Commands}}{{"\t"}}{{join .Names ", "}}{{"\t"}}{{.Usage}}{{if .Flags}}

{{"\t\t"}}FLAGS:
{{range .Flags}}{{"\t\t\t"}}{{.}}
{{end}}{{end}}
{{end}}{{end}}{{if .Flags}}MISC OPTIONS:
   {{range category .Flags "misc"}}{{.}}
   {{end}}{{end}}{{if .Copyright }}
COPYRIGHT:
   {{.Copyright}}
   {{end}}
`
}

var VersionHash string

func NewApp() (app *cli.App, cmd *Commands) {

	var funcMap = template.FuncMap{
		"category": filterCategory,
		"join":     strings.Join,
	}

	flagCategories = map[string]string{}

	for _, f := range []string{"help, h", "version, v"} {
		flagCategories[f] = "misc"
	}

	cmd = &Commands{}
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
		},
		Action: func(c *cli.Context) error {
			cli.ShowAppHelpAndExit(c, 0)
			return nil
		},
		Commands: []cli.Command{
			{
				Name:    "mount",
				Aliases: []string{"m"},
				Usage:   "to mount a folder",
				Action: func(c *cli.Context) error {
					cmd.IsMount = true
					twig.SetDebug(c.IsSet("debug"))
					// Populate and parse flags.
					flags, err := PopulateMountFlags(c)
					if err != nil {
						cause := errors.Cause(err)
						if os.IsPermission(cause) {
							fmt.Print("\nSeems like fusera doesn't have permissions to read a file!")
							fmt.Printf("\nTry changing the permissions with chmod +r path/to/file\n")
						}
						fmt.Printf("\ninvalid arguments: %s\n\n", errors.Cause(err))
						twig.Debugf("%+#v", err.Error())
						return err
					}
					defer func() {
						time.Sleep(time.Second)
						flags.Cleanup()
					}()
					twig.Debugf("accs: %s", flags.Acc)
					cmd.Flags = flags
					return nil
				},
				Flags: []cli.Flag{
					cli.StringFlag{
						Name:   "ngc",
						Usage:  "path to file that authenticates access",
						EnvVar: "DBGAP_CREDENTIALS",
					},
					cli.StringFlag{
						Name:   "acc",
						Usage:  "comma separated list of accessions",
						EnvVar: "DBGAP_ACC",
					},
					cli.StringFlag{
						Name:   "acc-file",
						Usage:  "path to a cart file, listing accession numbers",
						EnvVar: "DBGAP_ACCFILE",
					},
					cli.StringFlag{
						Name:   "loc",
						Usage:  "preferred region",
						EnvVar: "DBGAP_LOC",
					},
					cli.BoolFlag{
						Name:   "debug",
						Usage:  "Enable debugging output.",
						EnvVar: "FUSERA_DEBUG",
					},
					cli.StringFlag{
						Name:   "endpoint",
						Usage:  "Change the endpoint fusera uses to communicate with NIH API. Only to be used for advanced purposes.",
						EnvVar: "DBGAP_ENDPOINT",
					},
					cli.IntFlag{
						Name:   "aws-batch",
						Usage:  "Adjust the amount of accessions fusera puts in one request to the Name Resolver API when using an aws location. Only to be used for advanced purposes.",
						Value:  50,
						EnvVar: "DBGAP_AWSBATCH",
					},
					cli.IntFlag{
						Name:   "gcp-batch",
						Usage:  "Adjust the amount of accessions fusera puts in one request to the Name Resolver API when using a gcp location. Only to be used for advanced purposes.",
						Value:  10,
						EnvVar: "DBGAP_GCPBATCH",
					},
				},
			},
			{
				Name:    "unmount",
				Aliases: []string{"u"},
				Usage:   "to unmount a folder",
				Action: func(c *cli.Context) error {
					cmd.IsUnmount = true
					if c.NArg() != 1 {
						fmt.Printf("\ninvalid arguments: %s\n\n", "must give a path to a folder to unmount")
						cli.ShowAppHelpAndExit(c, 1)
					}
					cmd.Path = c.Args().First()
					twig.SetDebug(c.IsSet("debug"))
					return nil
				},
				Flags: []cli.Flag{
					cli.BoolFlag{
						Name:   "debug",
						Usage:  "Enable debugging output.",
						EnvVar: "FUSERA_DEBUG",
					},
				},
			},
		},
	}

	cli.HelpPrinter = func(w io.Writer, templ string, data interface{}) {
		w = tabwriter.NewWriter(w, 1, 8, 2, ' ', 0)
		var tmplGet = template.Must(template.New("help").Funcs(funcMap).Parse(templ))
		tmplGet.Execute(w, app)
	}

	return
}

type Commands struct {
	IsMount   bool
	Flags     *Flags
	IsUnmount bool
	Path      string
}

type Flags struct {
	Ngc  []byte
	Acc  map[string]bool
	Loc  string
	Path string

	MountOptions      map[string]string
	MountPoint        string
	MountPointArg     string
	MountPointCreated string

	DirMode  os.FileMode
	FileMode os.FileMode
	Uid      uint32
	Gid      uint32

	Debug    bool
	Endpoint string
	AwsBatch int
	GcpBatch int
}

func (f *Flags) Cleanup() {
	if f.MountPointCreated != "" && f.MountPointCreated != f.MountPointArg {
		err := os.Remove(f.MountPointCreated)
		if err != nil {
			twig.Debugf("rmdir %v = %v", f.MountPointCreated, err)
		}
	}
}

// Add the flags accepted by run to the supplied flag set, returning the
// variables into which the flags will parse.
func PopulateMountFlags(c *cli.Context) (ret *Flags, err error) {
	if c.NArg() != 1 {
		return nil, errors.New("must give a path to a folder to mount")
	}
	uid, gid := MyUserAndGroup()
	f := &Flags{
		Acc: make(map[string]bool),
		// File system
		MountOptions: make(map[string]string),
		DirMode:      0555,
		FileMode:     0444,
		Uid:          uint32(uid),
		Gid:          uint32(gid),
		// Debugging,
		Debug:    c.Bool("debug"),
		Endpoint: c.String("endpoint"),
		AwsBatch: 50,
		GcpBatch: 10,
	}
	if c.IsSet("aws-batch") {
		f.AwsBatch = c.Int("aws-batch")
	}
	if c.IsSet("gcp-batch") {
		f.GcpBatch = c.Int("gcp-batch")
	}
	ngcpath := c.String("ngc")
	if ngcpath != "" {
		// we were given a path to an ngc file. Let's read it.
		var data []byte
		if strings.HasPrefix(ngcpath, "http") {
			// we were given a url on s3.
			data, err = awsutil.ReadNgcFile(ngcpath)
			if err != nil {
				return nil, errors.Wrapf(err, "couldn't open ngc file at: %s", ngcpath)
			}
		} else {
			data, err = ioutil.ReadFile(ngcpath)
			if err != nil {
				return nil, errors.Wrapf(err, "couldn't open ngc file at: %s", ngcpath)
			}
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
	loc := c.String("loc")
	if !c.IsSet("loc") {
		loc, err = awsutil.ResolveRegion()
		if err != nil {
			return nil, err
		}
	}
	ok := awsutil.IsLocation(loc)
	if !ok {
		return nil, errors.Errorf("gave location of %s, location must match one of these possibilities:\n%s", loc, awsutil.IncorrectLocationMessage)
	}
	f.Loc = loc

	f.MountPointArg = c.Args().First()
	f.MountPoint = f.MountPointArg

	defer func() {
		if err != nil {
			f.Cleanup()
		}
	}()

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

func MyUserAndGroup() (int, int) {
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
	for _, a := range accs {
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
