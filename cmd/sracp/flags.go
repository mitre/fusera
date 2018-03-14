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
	"io"
	"io/ioutil"
	"os"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/mattrbianchi/twig"
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
		Name:     "sracopy",
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
			// Debugging
			/////////////////////////
			cli.BoolFlag{
				Name:  "debug",
				Usage: "Enable debugging output.",
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
	Help bool
	// Fusera flags
	Ngc  []byte
	Acc  map[string]bool
	Loc  string
	Path string
	// SRR# has a map of file names that map to urls where the data is
	Urls map[string]map[string]string

	// Debugging
	Debug bool
}

func reconcileAccs(data []byte) []string {
	accs_csv := strings.Split(string(data), ",")
	if len(accs_csv) != 1 {
		return accs_csv
	}
	accs_tsv := strings.Split(string(data), " ")
	return accs_tsv
}

// Add the flags accepted by run to the supplied flag set, returning the
// variables into which the flags will parse.
func PopulateFlags(c *cli.Context) (ret *Flags, err error) {
	f := &Flags{
		Acc: make(map[string]bool),
		// Debugging,
		Debug: c.Bool("debug"),
		Help:  c.Bool("help"),
	}
	if f.Help {
		return f, nil
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

	f.Path = c.Args()[0]
	twig.SetDebug(f.Debug)

	return f, nil
}
