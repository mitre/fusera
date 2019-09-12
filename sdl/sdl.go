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

package sdl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httputil"

	"github.com/mitre/fusera/info"

	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/fuseralib"
	"github.com/pkg/errors"
)

var (
	defaultEndpoint = fmt.Sprintf("https://www.ncbi.nlm.nih.gov/Traces/sdl/%s/retrieve", info.SdlVersion)
)

// SDL SDL is the main object to use when wanting to interact with the SDL API.
type SDL struct {
	URL   string
	Param *Param
}

// NewSDL Creates a new SDL with default values already set.
func NewSDL() *SDL {
	return &SDL{
		URL:   defaultEndpoint,
		Param: &Param{},
	}
}

// SignAllInBatch The function to call to get information on all the accessions, but in batches to avoid overloading the SDL API.
func (s *SDL) SignAllInBatch(batch int) ([]*fuseralib.Accession, error) {
	accessions := []*fuseralib.Accession{}
	var rootErr []byte
	// loop until all accessions are asked for
	dot := batch
	i := 0
	for dot < len(s.Param.Acc) {
		aa, err := signListed(s.URL, s.Param.Acc[i:dot], s.Param)
		if err != nil {
			rootErr = append(rootErr, []byte(fmt.Sprintln(err.Error()))...)
			rootErr = append(rootErr, []byte("List of accessions that failed in this batch:\n")...)
			rootErr = append(rootErr, []byte(fmt.Sprintln(s.Param.Acc[i:dot]))...)
			if !flags.Silent {
				fmt.Println(string(rootErr))
			}
		} else {
			accessions = append(accessions, aa...)
		}
		i = dot
		dot += batch
	}
	aa, err := signListed(s.URL, s.Param.Acc[i:], s.Param)
	if err != nil {
		rootErr = append(rootErr, []byte(fmt.Sprintln(err.Error()))...)
		rootErr = append(rootErr, []byte("List of accessions that failed in this batch:\n")...)
		rootErr = append(rootErr, []byte(fmt.Sprintln(s.Param.Acc[i:]))...)
		if !flags.Silent {
			fmt.Println(string(rootErr))
		}
	} else {
		accessions = append(accessions, aa...)
	}

	return accessions, nil
}

func signListed(url string, aa []string, param *Param) ([]*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := param.AddGlobals(writer)
	if err != nil {
		return nil, err
	}
	err = addAccessions(writer, aa)
	if err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, errors.New("could not close multipart.Writer")
	}

	return makeRequest(url, body, writer)
}

// Sign The function to call to sign a single accession.
func (s *SDL) Sign(accession string) (*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := s.Param.AddGlobals(writer)
	if err != nil {
		return nil, err
	}
	err = addAccessions(writer, []string{accession})
	if err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, errors.New("could not close multipart.Writer")
	}
	accs, err := makeRequest(s.URL, body, writer)
	if err != nil {
		return nil, err
	}
	if len(accs) != 1 {
		return nil, errors.New("SDL API returned more accessions than requested")
	}
	return accs[0], nil
}

// AddIdent Adds an ident parameter to a link to fulfill the demand of a Compute Environment Required file link.
func (s *SDL) AddIdent(link string) (string, error) {
	token, err := s.Param.Location.Locality()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s&ident=%s", link, string(token)), nil
}

// SignAll Asks the SDL API to return locations (including signed links) for all the accessions, typically called on start up of Fusera when the eager flag has been set.
func (s *SDL) SignAll() ([]*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := s.Param.AddGlobals(writer)
	if err != nil {
		return nil, err
	}
	err = addAccessions(writer, s.Param.Acc)
	if err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, errors.New("could not close multipart.Writer")
	}

	return makeRequest(s.URL, body, writer)
}

func makeRequest(url string, body *bytes.Buffer, writer *multipart.Writer) ([]*fuseralib.Accession, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, errors.New("can't create request to SDL API")
	}
	req.Header.Set("User-Agent", info.BinaryName+"-"+info.Version)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if flags.Verbose {
		reqdump, err := httputil.DumpRequestOut(req, true)
		if err != nil {
			return nil, errors.New("INTERNAL ERROR: failed to print request to API for verbose")
		}
		fmt.Println("REQUEST TO API")
		fmt.Println(string(reqdump))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("can't send request to SDL API")
	}
	defer resp.Body.Close()
	if flags.Verbose {
		resdump, err := httputil.DumpResponse(resp, true)
		if err != nil {
			return nil, errors.New("INTERNAL ERROR: failed to print response from API for verbose")
		}
		fmt.Println("RESPONSE FROM API")
		fmt.Println(string(resdump))
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr apiError
		err := json.NewDecoder(resp.Body).Decode(&apiErr)
		if err != nil {
			response, _ := ioutil.ReadAll(resp.Body)
			return nil, errors.Errorf("failed to decode error message from SDL API after getting HTTP status: %d: %s\nResponse:%v\n", resp.StatusCode, resp.Status, string(response))
		}
		return nil, errors.Errorf("SDL API returned error: %d: %s", apiErr.Status, apiErr.Message)
	}
	message := VersionWrap{}
	err = json.NewDecoder(resp.Body).Decode(&message)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response from Name Resolver API")
	}

	return validate(message)
}

func validate(message VersionWrap) ([]*fuseralib.Accession, error) {
	err := message.Validate()
	if err != nil {
		return nil, err
	}
	dup := map[string]bool{}
	list := make([]*fuseralib.Accession, 0, len(message.Result))
	for i, a := range message.Result {
		err := message.Result[i].Validate(dup)
		if err != nil {
			if !flags.Silent {
				fmt.Println(err.Error())
			}
			errAcc := &fuseralib.Accession{ID: message.Result[i].ID, Files: make(map[string]fuseralib.File)}
			errAcc.AppendError(err.Error())
			list = append(list, errAcc)
			continue
		}
		list = append(list, a.Transfigure())
	}
	return list, nil
}

// Retrieve The function to call to get information on a single accession.
func (s *SDL) Retrieve(accession string) (*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := s.Param.AddGlobals(writer)
	if err != nil {
		return nil, err
	}
	err = addAccessions(writer, []string{accession})
	if err != nil {
		return nil, err
	}
	err = addMetaOnly(writer)
	if err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, errors.New("could not close multipart.Writer")
	}
	accs, err := makeRequest(s.URL, body, writer)
	if err != nil {
		return nil, err
	}
	if len(accs) != 1 {
		return nil, errors.New("SDL API returned more accessions than requested")
	}
	return accs[0], nil
}

// RetrieveAll The function to call to get information on all the accessions.
func (s *SDL) RetrieveAll() ([]*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := s.Param.AddGlobals(writer)
	if err != nil {
		return nil, err
	}
	err = addAccessions(writer, s.Param.Acc)
	if err != nil {
		return nil, err
	}
	err = addMetaOnly(writer)
	if err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, errors.New("could not close multipart.Writer")
	}

	return makeRequest(s.URL, body, writer)
}
