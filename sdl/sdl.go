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
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/mitre/fusera/awsutil"

	"github.com/mitre/fusera/flags"
	"github.com/mitre/fusera/fuseralib"
	"github.com/pkg/errors"
)

var (
	defaultEndpoint = "https://www.ncbi.nlm.nih.gov/Traces/sdl/1/retrieve"
)

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

// Retrieve Calls the retrieve endpoint on SDL with the list of accessions given.
func (c *Client) Retrieve(accessions []string) ([]*fuseralib.Accession, error) {
	return c.makeRequest(accessions, true)
}

// Sign has the SDL API create signed urls for all files under the given accession.
func (c *Client) Sign(accession string) (*fuseralib.Accession, error) {
	accs, err := c.makeRequest([]string{accession}, false)
	if err != nil {
		return nil, err
	}
	if len(accs) != 1 {
		return nil, errors.New("SDL API returned more accessions than requested")
	}
	return accs[0], nil
}

func (c *Client) makeRequest(accessions []string, meta bool) ([]*fuseralib.Accession, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer, err := c.addParams(writer, accessions, meta)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.url, body)
	if err != nil {
		return nil, errors.New("can't create request to SDL API")
	}
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
		var errPayload Payload
		err := json.NewDecoder(resp.Body).Decode(&errPayload)
		if err != nil {
			response, _ := ioutil.ReadAll(resp.Body)
			return nil, errors.Errorf("failed to decode error message from SDL API after getting HTTP status: %d: %s\nResponse:%v\n", resp.StatusCode, resp.Status, string(response))
		}
		return nil, errors.Errorf("SDL API returned error: %d: %s", errPayload.Status, errPayload.Message)
	}
	var payload []Payload
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response from Name Resolver API")
	}

	return sanitize(payload)
}

// NewEagerClient creates a client that has the SDL API sign urls ahead of time when retrieving data for accessions.
func NewEagerClient(url, loc string, ngc []byte, types map[string]bool) *EagerClient {
	if url == "" {
		url = defaultEndpoint
	}
	if loc == "" {
		return nil
	}
	return &EagerClient{
		Client: Client{
			url:      url,
			location: loc,
			types:    types,
			ngc:      ngc,
		},
	}
}

// EagerClient A client that "eagerly" asks the API to go ahead and
// create signed urls for all the files under all the accessions queried
// through the retrieve endpoint.
type EagerClient struct {
	Client
}

// Retrieve has the SDL API return meta information for all files under the given accessions.
// accessions: the accessions to get metadata for.
func (c *EagerClient) Retrieve(accessions []string) ([]*fuseralib.Accession, error) {
	return c.makeRequest(accessions, false)
}

// NewGCPClient creates a client that has the SDL API sign urls ahead of time when retrieving data for accessions.
func NewGCPClient(url string, ngc []byte, types map[string]bool) *GCPClient {
	if url == "" {
		url = defaultEndpoint
	}
	return &GCPClient{
		Client: Client{
			url:   url,
			ngc:   ngc,
			types: types,
		},
	}
}

// GCPClient handles setting the parameters properly for when Google is the cloud platform.
type GCPClient struct {
	Client
}

// Sign gets a signed url for a file in a Google cloud region.
func (c *GCPClient) Sign(accession string) (*fuseralib.Accession, error) {
	// Get an instance token, set it to location.
	platform, err := awsutil.FindLocation()
	if err != nil {
		return nil, errors.New("Could not refresh GCP instance token for sdl location")
	}
	c.location = string(platform.InstanceToken[:])
	accs, err := c.makeRequest([]string{accession}, false)
	if err != nil {
		return nil, err
	}
	for _, a := range accs {
		if a.ID == accession {
			return a, nil
		}
	}
	return nil, errors.New("SDL API did not return requested accession")
}

func sanitize(payload []Payload) ([]*fuseralib.Accession, error) {
	successfulAccessionExists := false
	accs := make(map[string]*fuseralib.Accession)
	for _, p := range payload {
		errmsg := ""
		if p.Status != http.StatusOK {
			// Something is wrong with the whole accession
			errmsg = fmt.Sprintf("Some errors were encountered with %s:\n", p.ID)
			errmsg = errmsg + fmt.Sprintf("%d\t%s\n", p.Status, p.Message)
			errAcc := &fuseralib.Accession{ID: p.ID, Files: make(map[string]fuseralib.File)}
			if !flags.Silent {
				fmt.Println(errmsg)
			}
			if a, ok := accs[p.ID]; ok {
				// so we have a duplicate acc...
				errAcc = a
			}
			errAcc.AppendError(errmsg)
			accs[errAcc.ID] = errAcc
			continue
		}
		// get existing acc or make a new one
		acc := &fuseralib.Accession{ID: p.ID, Files: make(map[string]fuseralib.File)}
		if a, ok := accs[p.ID]; ok {
			// so we have a duplicate acc...
			acc = a
		}
		for _, f := range p.Files {
			// Checking if something is wrong with the individual files
			if f.Name == "" {
				if !flags.Silent {
					fmt.Printf("API returned no name field for file: %v\n", f)
				}
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
		err = errors.New("API returned no usable accessions")
	}
	list := make([]*fuseralib.Accession, 0, len(accs))
	for _, v := range accs {
		list = append(list, v)
	}
	return list, err
}

func (c *Client) addParams(writer *multipart.Writer, accessions []string, meta bool) (*multipart.Writer, error) {
	if err := c.addLocation(writer); err != nil {
		return nil, err
	}
	if err := c.addNgc(writer); err != nil {
		return nil, err
	}
	if err := c.addFileType(writer); err != nil {
		return nil, err
	}
	if meta {
		if err := c.addMetaOnly(writer); err != nil {
			return nil, err
		}
	}
	if accessions != nil && len(accessions) > 0 {
		if err := c.addAccessions(writer, accessions); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, errors.New("could not close multipart.Writer")
	}
	return writer, nil
}

// Payload The JSON response from SDL's retrieve endpoint.
type Payload struct {
	ID      string           `json:"accession,omitempty"`
	Status  int              `json:"status,omitempty"`
	Message string           `json:"message,omitempty"`
	Files   []fuseralib.File `json:"files,omitempty"`
}

func (c *Client) addFileType(writer *multipart.Writer) error {
	if c.types != nil {
		tt := make([]string, 0)
		for k := range c.types {
			tt = append(tt, k)
		}
		typesField := strings.Join(tt, ",")
		if err := writer.WriteField("filetype", typesField); err != nil {
			return errors.New("could not write filetype field to multipart.Writer")
		}
	}
	return nil
}

func (c *Client) addNgc(writer *multipart.Writer) error {
	if c.ngc != nil {
		// handle ngc bytes
		part, err := writer.CreateFormFile("ngc", "ngc")
		if err != nil {
			return errors.Wrapf(err, "couldn't create form file for ngc")
		}
		_, err = io.Copy(part, bytes.NewReader(c.ngc))
		if err != nil {
			return errors.Errorf("couldn't copy ngc contents: %s into multipart file to make request", c.ngc)
		}
	}
	return nil
}

func (c *Client) addAccessions(writer *multipart.Writer, accessions []string) error {
	for _, acc := range accessions {
		if err := writer.WriteField("acc", acc); err != nil {
			return errors.New("could not write acc field to multipart.Writer")
		}
	}
	return nil
}

func (c *Client) addLocation(writer *multipart.Writer) error {
	if err := writer.WriteField("location", c.location); err != nil {
		return errors.New("could not write location field to multipart.Writer")
	}
	return nil
}

func (c *Client) addMetaOnly(writer *multipart.Writer) error {
	if err := writer.WriteField("meta-only", "yes"); err != nil {
		return errors.New("could not write meta-only field to multipart.Writer")
	}
	return nil
}
