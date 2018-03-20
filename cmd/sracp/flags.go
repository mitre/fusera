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
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
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
		Name:     "sracp",
		Version:  "0.0.-" + VersionHash,
		Usage:    "",
		HideHelp: true,
		Writer:   os.Stderr,
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "help, h",
				Usage: "Print this help text and exit successfully.",
			},
			cli.StringFlag{
				Name:   "ngc",
				Usage:  "path to an ngc file that contains authentication info.",
				EnvVar: "DBGAP_CREDENTIALS,SRACP_NGCFILE,SRACP_CREDENTIALS",
			},
			cli.StringFlag{
				Name:   "acc",
				Usage:  "comma separated list of SRR#s that are to be mounted.",
				EnvVar: "DBGAP_ACC,SRACP_ACC",
			},
			cli.StringFlag{
				Name:   "acc-file",
				Usage:  "path to file with comma or space separated list of SRR#s that are to be mounted.",
				EnvVar: "DBGAP_ACCFILE,SRACP_ACCFILE",
			},
			cli.StringFlag{
				Name:   "loc",
				Usage:  "preferred region.",
				EnvVar: "DBGAP_LOC,SRACP_LOC",
			},
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

	for _, f := range []string{"help, h", "debug", "version, v"} {
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
	Ngc   []byte
	Acc   map[string]bool
	Loc   string
	Path  string
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
	if len(c.Args()) != 1 {
		return nil, errors.New("must give a path to copy files to")
	}
	f := &Flags{
		Acc:  make(map[string]bool),
		Path: c.Args()[0],
		// Debugging,
		Debug: c.Bool("debug"),
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
		// maybe we are on an AWS instance and can resolve what region we are in.
		// let's try it out and if we timeout we'll return an error.
		// use this url: http://169.254.169.254/latest/dynamic/instance-identity/document
		resp, err := http.Get("http://169.254.169.254/latest/dynamic/instance-identity/document")
		if err != nil {
			return nil, errors.Wrapf(err, "location was not provided, fusera attempted to resolve region but encountered an error, this feature only works when fusera is on an amazon instance")
		}
		if resp.StatusCode != http.StatusOK {
			return nil, errors.Errorf("issue trying to resolve region, got: %d: %s", resp.StatusCode, resp.Status)
		}
		var payload struct {
			Region string `json:"region"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		if err != nil {
			return nil, errors.New("issue trying to resolve region, couldn't decode response from amazon")
		}
		if payload.Region == "" {
			return nil, errors.New("issue trying to resolve region, amazon returned empty region")
		}
		loc = "s3." + payload.Region
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
	twig.SetDebug(f.Debug)
	return f, nil
}
