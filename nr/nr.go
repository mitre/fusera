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
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// ResolveNames uses the SRA Data Locator API to retrieve files for accessions
// url: the endpoint for ResolveNames to use, otherwise default will be used.
// loc: the location to request the files to be in.
// ngc: the bytes that represent an ngc file, authorizing access to accessions
// batch: the number of accessions to ask for at once in one request.
// accs: the accessions to resolve names for.
func ResolveNames(url string, batch int, meta bool, loc string, ngc []byte, accs, types map[string]bool) (map[string]*Accession, string, error) {
	if accs == nil {
		return nil, "", errors.New("must provide accessions to pass to Name Resolver API")
	}
	if loc == "" {
		return nil, "", errors.New("must provide a location to pass to Name Resolver API")
	}
	if batch < 1 {
		return nil, "", errors.Errorf("must provide a valid batch number, gave: %d", batch)
	}
	if url == "" {
		url = "https://www.ncbi.nlm.nih.gov/Traces/sdl/1/retrieve"
	}
	payload := make([]Payload, 0, len(accs))
	batchCount := 0
	totalCount := 0
	var body *bytes.Buffer
	var writer *multipart.Writer
	totalAccs := len(accs)
	var currentAccsInBatch []string
	var report string
	for acc := range accs {
		batchCount++
		totalCount++
		if batchCount == 1 {
			body = &bytes.Buffer{}
			writer = multipart.NewWriter(body)
			if err := writeFields(writer, meta, ngc, loc, types); err != nil {
				return nil, "", err
			}
			currentAccsInBatch = make([]string, 0, batch)
		}
		if err := writer.WriteField("acc", acc); err != nil {
			return nil, "", errors.Errorf("could not write acc field to multipart.Writer for accession: %s", acc)
		}
		currentAccsInBatch = append(currentAccsInBatch, acc)
		if batchCount == batch || batchCount == totalAccs || totalCount == totalAccs {
			if err := writer.Close(); err != nil {
				return nil, "", errors.New("internal error: could not close multipart.Writer")
			}
			p, err := makeBatchRequest(url, writer, body)
			if err != nil {
				report += fmt.Sprintln("encountered an issue in one of the batches:")
				report += fmt.Sprintln(err.Error())
				report += fmt.Sprintf("Total number of accessions that failed in this batch: %d\n", len(currentAccsInBatch))
				report += fmt.Sprintf("Accessions in batch that failed: %s\n", strings.Join(currentAccsInBatch, "\n"))
				batchCount = 0
				continue
			}
			payload = append(payload, p...)
			batchCount = 0
		}
	}
	accessions, err := sanitize(payload)
	return accessions, report, err
}

// SignAccession has the SDL API create signed urls for all files under the given accession.
// url: the endpoint for the SDL API.
// loc: the location to request the files to be in.
// accs: the accessions to resolve names for.
// ngc: the bytes that represent an ngc file, authorizing access to accessions.
// types: the file types desired.
func SignAccession(url, loc, acc string, ngc []byte, types map[string]bool) (*Accession, error) {
	if acc == "" {
		return nil, errors.New("must provide accession to pass to SDL API")
	}
	if loc == "" {
		return nil, errors.New("must provide a location to pass to SDL API")
	}
	if url == "" {
		url = "https://www.ncbi.nlm.nih.gov/Traces/sdl/1/retrieve"
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("location", loc); err != nil {
		return nil, errors.New("could not write loc field to multipart.Writer")
	}
	if err := writer.WriteField("acc", acc); err != nil {
		return nil, errors.New("could not write acc field to multipart.Writer")
	}
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
	if err := writer.Close(); err != nil {
		return nil, errors.New("could not close multipart.Writer")
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, errors.New("can't create request to Name Resolver API")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("can't resolve acc names")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errPayload Payload
		err := json.NewDecoder(resp.Body).Decode(&errPayload)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to decode error message from SDL API after getting HTTP status: %d: %s", resp.StatusCode, resp.Status)
		}
		return nil, errors.Errorf("SDL API returned error: %d: %s", errPayload.Status, errPayload.Message)
	}
	var payload []Payload
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response from Name Resolver API")
	}

	accessions, err := sanitize(payload)
	if err != nil {
		return nil, err
	}
	if _, ok := accessions[acc]; !ok {
		return nil, errors.New("SDL API did not return requested accession")
	}
	return accessions[acc], nil
}

func makeBatchRequest(url string, writer *multipart.Writer, body io.Reader) ([]Payload, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, errors.New("can't create request to Name Resolver API")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	// twig.Debugf("HTTP REQUEST:\n %+v", req)
	// implement a retry
	retried := false
	var resp *http.Response
	for {
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			return nil, errors.Wrap(err, "network error encountered when making API request")
		}
		if resp.StatusCode != http.StatusOK {
			if !retried {
				retried = true
				resp.Body.Close()
				continue
			}
			var errPayload Payload
			err := json.NewDecoder(resp.Body).Decode(&errPayload)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to decode error message from SDL API after getting HTTP status: %d: %s", resp.StatusCode, resp.Status)
			}
			return nil, errors.Errorf("SDL API returned error: %d: %s", errPayload.Status, errPayload.Message)
		}
		break
	}
	defer resp.Body.Close()

	var payload []Payload
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response from Name Resolver API")
	}
	return payload, nil
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
			acc.Files[f.Name] = f
		}
		successfulAccessionExists = true
		accs[acc.ID] = acc
	}
	var err error
	if !successfulAccessionExists {
		err = errors.New("API returned no accessions")
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
	Type           string    `json:"type,omitempty"`
	ModifiedDate   time.Time `json:"modificationDate,omitempty"`
	Md5Hash        string    `json:"md5,omitempty"`
	Link           string    `json:"link,omitempty"`
	ExpirationDate time.Time `json:"expirationDate,omitempty"`
	Service        string    `json:"service,omitempty"`
}

func writeFields(writer *multipart.Writer, meta bool, ngc []byte, loc string, types map[string]bool) error {
	if err := writer.WriteField("location", loc); err != nil {
		return errors.New("could not write loc field to multipart.Writer")
	}
	if meta {
		if err := writer.WriteField("meta-only", "yes"); err != nil {
			return errors.New("could not write meta-only field to multipart.Writer")
		}
	}
	if ngc != nil {
		// handle ngc bytes
		part, err := writer.CreateFormFile("ngc", "ngc")
		if err != nil {
			return errors.Wrapf(err, "couldn't create form file for ngc")
		}
		_, err = io.Copy(part, bytes.NewReader(ngc))
		if err != nil {
			return errors.Errorf("couldn't copy ngc contents: %s into multipart file to make request", ngc)
		}

	}
	if types != nil {
		tt := make([]string, 0)
		for k := range types {
			tt = append(tt, k)
		}
		typesField := strings.Join(tt, ",")
		if err := writer.WriteField("filetype", typesField); err != nil {
			return errors.New("could not write filetype field to multipart.Writer")
		}
	}
	return nil
}
