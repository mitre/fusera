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

// SDL is an interface that describes the functions of the SDL API.
type Retriever interface {
	Retrieve(accessions []string) ([]*Accession, error)
	RetrieveAll(accession string) (*Accession, error)
}

// SDL SDL is the main object to use when wanting to interact with the SDL API.
type SDL struct {
	Client Retriever
	Param  *Param
}

// Retrieve The function to call to get information on a single accession.
func (s *SDL) Retrieve(accession string) (*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := s.Param.Add(writer)
	if err != nil {
		return nil, err
	}
	err = addAccessions(writer, []string{accession})
	if err != nil {
		return nil, err
	}
	accs, err := s.Client.makeRequest(body, writer)
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
	writer, err := s.Param.Add(writer)
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

	return s.Client.makeRequest(body, writer)
}

// SignAll Asks the SDL API to return locations (including signed links) for all the accessions, typically called on start up of Fusera when the eager flag has been set.
func (s *SDL) SignAll() ([]*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := s.Param.Add(writer)
	if err != nil {
		return nil, err
	}
	err = addAccessions(writer, s.Param.Acc)
	if err != nil {
		return nil, err
	}

	return s.Client.makeRequest(body, writer)
}

// Sign The function to call to sign a single accession.
func (s *SDL) Sign(accession string) (*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := s.Param.Add(writer)
	if err != nil {
		return nil, err
	}
	err = addAccessions(writer, []string{accession})
	if err != nil {
		return nil, err
	}
	accs, err := s.Client.makeRequest(body, writer)
	if err != nil {
		return nil, err
	}
	if len(accs) != 1 {
		return nil, errors.New("SDL API returned more accessions than requested")
	}
	return accs[0], nil
}

// Client is an implementation of the fuseralib.Resolver interface
// that uses the SDL API to provide metadata, locations, and proper access
// to files in the SDL system.
type Client struct {
	url      string
	location string
	types    map[string]bool
	batch    int
	ngc      []byte
}

// NewClient creates a client with given parameters to communicate with the SDL API.
func NewClient(url, loc string, ngc []byte, types map[string]bool) *Client {
	if url == "" {
		url = defaultEndpoint
	}
	if loc == "" {
		return nil
	}
	return &Client{
		url:      url,
		location: loc,
		types:    types,
		ngc:      ngc,
	}
}

func (c *Client) makeRequest(body *bytes.Buffer, writer *multipart.Writer) ([]*fuseralib.Accession, error) {
	req, err := http.NewRequest("POST", c.url, body)
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
		var apiErr ApiError
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

	return sanitize(message)
}

func sanitize(message VersionWrap) ([]*fuseralib.Accession, error) {
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
		}
		list = append(list, a.Transfigure())
	}
	return list, nil
}

// // NewGCPClient creates a client that has the SDL API sign urls ahead of time when retrieving data for accessions.
// func NewGCPClient(url string, ngc []byte, types map[string]bool) *GCPClient {
// 	if url == "" {
// 		url = defaultEndpoint
// 	}
// 	return &GCPClient{
// 		Client: Client{
// 			url:   url,
// 			ngc:   ngc,
// 			types: types,
// 		},
// 	}
// }

// // GCPClient handles setting the parameters properly for when Google is the cloud platform.
// type GCPClient struct {
// 	Client
// }

// // Sign gets a signed url for a file in a Google cloud region.
// func (c *GCPClient) Sign(accession string) (*fuseralib.Accession, error) {
// 	// Get an instance token, set it to location.
// 	platform, err := gps.FindLocation()
// 	if err != nil {
// 		return nil, errors.New("Could not refresh GCP instance token for sdl location")
// 	}
// 	c.location = string(platform.InstanceToken[:])
// 	accs, err := c.makeRequest([]string{accession}, false)
// 	if err != nil {
// 		return nil, err
// 	}
// 	for _, a := range accs {
// 		if a.ID == accession {
// 			return a, nil
// 		}
// 	}
// 	return nil, errors.New("SDL API did not return requested accession")
// }
