// Modifications Copyright 2018 The MITRE Corporation
// Author: Matthew Bianchi
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

package nr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/mattrbianchi/twig"
	"github.com/pkg/errors"
)

func ResolveNames(url, loc string, ngc []byte, accs map[string]bool) (map[string]*Accession, error) {
	if url == "" {
		url = "https://www.ncbi.nlm.nih.gov/Traces/names/names.fcgi"
		twig.Debugf("Name Resolver endpoint was empty, using default: %s", url)
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if ngc != nil {
		// handle ngc bytes
		part, err := writer.CreateFormFile("ngc", "ngc")
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't create form file for ngc")
		}
		_, err = io.Copy(part, bytes.NewReader(ngc))
		if err != nil {
			return nil, errors.Errorf("couldn't copy ngc contents: %s into multipart file to make request", ngc)
		}

	}
	if err := writer.WriteField("version", "xc-1.0"); err != nil {
		return nil, errors.New("could not write version field to multipart.Writer")
	}
	if err := writer.WriteField("format", "json"); err != nil {
		return nil, errors.New("could not write format field to multipart.Writer")
	}
	if loc != "" {
		if err := writer.WriteField("location", loc); err != nil {
			return nil, errors.New("could not write loc field to multipart.Writer")
		}
	}
	if accs != nil {
		for acc, _ := range accs {
			if err := writer.WriteField("acc", acc); err != nil {
				return nil, errors.New("could not write acc field to multipart.Writer")
			}
		}
	}
	twig.Debug("version: xc-1.0")
	twig.Debug("format: json")
	twig.Debugf("location: %s", loc)
	twig.Debugf("acc: %v", accs)
	if err := writer.Close(); err != nil {
		return nil, errors.New("could not close multipart.Writer")
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, errors.New("can't create request to Name Resolver API")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	twig.Debugf("HTTP REQUEST:\n %+v", req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("can't resolve acc names")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("encountered error from Name Resolver API: %s", resp.Status)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		return nil, errors.Errorf("Name Resolver API gave incorrect Content-Type: %s", ct)
	}

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.New("fatal error when trying to read response from Name Resolver API")
	}
	content := string(bytes)
	twig.Debugf("Response Body from API:\n%s", content)
	var payload []Payload
	err = json.Unmarshal(bytes, &payload)
	if err != nil {
		var errPayload Payload
		err = json.Unmarshal(bytes, &errPayload)
		if err != nil {
			return nil, errors.Errorf("could not understand response from Name Resolver API: %s\n", content)
		}
		return nil, errors.Errorf("encountered error from Name Resolver API: %d: %s\n", errPayload.Status, errPayload.Message)
	}

	accessions, err := sanitize(payload)

	return accessions, err
}

func sanitize(payload []Payload) (map[string]*Accession, error) {
	successfulAccessionExists := false
	accs := make(map[string]*Accession)
	for _, p := range payload {
		errmsg := ""
		if p.Status != http.StatusOK {
			// Something is wrong with the whole accession
			errmsg = fmt.Sprintf("Some errors were encountered with %s:\n", p.ID)
			errmsg = errmsg + fmt.Sprintf("%d\t%s\n", p.Status, p.Message)
			twig.Debug(errmsg)
			errAcc := &Accession{ID: p.ID, Files: make(map[string]File)}
			if a, ok := accs[p.ID]; ok {
				// so we have a duplicate acc...
				errAcc = a
			}
			errAcc.AppendError(errmsg)
			accs[errAcc.ID] = errAcc
			continue
		}
		// get existing acc or make a new one
		acc := &Accession{ID: p.ID, Files: make(map[string]File)}
		if a, ok := accs[p.ID]; ok {
			// so we have a duplicate acc...
			acc = a
		}
		for _, f := range p.Files {
			// Checking if something is wrong with the individual files
			if f.Name == "" {
				acc.AppendError(fmt.Sprintf("API returned no name field for file: %v\n", f))
				accs[acc.ID] = acc
				continue
			}
			if f.Link == "" {
				acc.AppendError(fmt.Sprintf("API returned no link for file: %s\n", f.Name))
				accs[acc.ID] = acc
				continue
			}
			// TODO: this is where we'll do HEAD calls on the files to check the validity of the URLs
			acc.Files[f.Name] = f
		}
		successfulAccessionExists = true
		accs[acc.ID] = acc
	}
	var err error
	if !successfulAccessionExists {
		err = errors.New("API returned no mountable accessions! Check error logs to resolve.\n")
	}
	return accs, err
}

type Payload struct {
	ID      string `json:"accession,omitempty"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Files   []File `json:"files,omitempty"`
}

type Accession struct {
	ID       string `json:"accession,omitempty"`
	errorLog string
	Files    map[string]File
}

func (a *Accession) ErrorLog() string {
	return a.errorLog
}

func (a *Accession) AppendError(message string) {
	a.errorLog += message
}

func (a *Accession) HasError() bool {
	return a.errorLog != ""
}

type File struct {
	Name           string    `json:"name,omitempty"`
	Size           string    `json:"size,omitempty"`
	ModifiedDate   time.Time `json:"modificationDate,omitempty"`
	Md5Hash        string    `json:"md5,omitempty"`
	Link           string    `json:"link,omitempty"`
	ExpirationDate time.Time `json:"expirationDate,omitempty"`
	Service        string    `json:"service,omitempty"`
}
