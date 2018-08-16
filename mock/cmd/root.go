// Modifications Copyright 2018 The MITRE Corporation
// Authors: Matthew Bianchi
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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	debug bool
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug output.")
	if err := viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug")); err != nil {
		panic("INTERNAL ERROR: could not bind debug flag to debug environment variable")
	}

	viper.AutomaticEnv()
}

var rootCmd = &cobra.Command{
	Use:   "mocksdlapi",
	Short: "A mock implementation of the SDL API.",
	Long:  ``,
	RunE:  run,
}

func run(cmd *cobra.Command, args []string) error {
	// Start up an http server and serve 5019 accessions named an+1 with n starting at 0
	r := mux.NewRouter()
	r.HandleFunc("/", HomeHandler)
	http.Handle("/", r)
	http.ListenAndServe(":8080", r)
	return nil
}

type payload struct {
	ID      string `json:"accession,omitempty"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Files   []file `json:"files,omitempty"`
}

type file struct {
	Name           string    `json:"name,omitempty"`
	Size           string    `json:"size,omitempty"`
	Type           string    `json:"type,omitempty"`
	ModifiedDate   time.Time `json:"modificationDate,omitempty"`
	Md5Hash        string    `json:"md5,omitempty"`
	Link           string    `json:"link,omitempty"`
	ExpirationDate time.Time `json:"expirationDate,omitempty"`
	Bucket         string    `json:"bucket,omitempty"`
	Key            string    `json:"key,omitempty"`
	Service        string    `json:"service,omitempty"`
}

// HomeHandler returns whatever JSON I want.
func HomeHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	response := make([]payload, 1, 1)
	for i := range response {
		response[i].ID = "a" + fmt.Sprintf("%d", i)
		response[i].Status = 200
		response[i].Files = make([]file, 1, 1)
		for j := range response[i].Files {
			response[i].Files[j].Name = "test.txt"
			response[i].Files[j].Bucket = "matt-first-test-bucket"
			response[i].Files[j].Key = "test.txt"
			response[i].Files[j].Size = "51"
			//response[i].Files[j].ExpirationDate = time.Now().Add(time.Hour)
		}
	}
	js, _ := json.Marshal(&response)
	if err := json.NewEncoder(w).Encode(&response); err != nil {
		panic("couldn't encode json")
	}
	fmt.Println(string(js))
}

// Execute runs the root command of mocksdlapi.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
