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
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/mattrbianchi/twig"
	"github.com/pkg/errors"
)

func ResolveNames(loc string, ngc []byte, accs map[string]bool) (map[string]Accession, error) {
	url := "https://www.ncbi.nlm.nih.gov/Traces/names_test/names.cgi"
	// url := "http://localhost:8000/"
	// acc := strings.Join(accs, ",")
	// fmt.Println("accs sent to name resolver: ", acc)
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
	// if acc != "" {
	// 	if err := writer.WriteField("acc", acc); err != nil {
	// 		panic("could not write acc field to multipart.Writer")
	// 	}
	// }
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
		return nil, errors.Errorf("error from Name Resolver API: %s", resp.Status)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		return nil, errors.Errorf("Name Resolver API gave incorrect Content-Type: %s", ct)
	}

	var payload []Payload
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response from Name Resolver API")
	}
	if j, err := json.MarshalIndent(payload, "", "\t"); err != nil {
		twig.Debug("failed to marshal response from Name Resolver API for debug logging")
	} else {
		twig.Debugf("Response from Name Resolver API:\n%s", string(j))
	}

	accessions := sanitize(payload)

	return accessions, nil
}

func sanitize(payload []Payload) map[string]Accession {
	accs := make(map[string]Accession)
	for _, p := range payload {
		// OK, so we don't want duplicate ACCs...
		// but if we get duplicates, I guess we could union the files given...
		// but then we don't want duplicate files...
		// fun...
		if p.Status != http.StatusOK {
			twig.Infof("issue with getting files for %s: %s", p.ID, p.Message)
			continue
		}
		// get existing acc or make a new one
		acc := Accession{ID: p.ID, Files: make(map[string]File)}
		if a, ok := accs[p.ID]; ok {
			// so we have a duplicate acc...
			acc = a
		}
		for _, f := range p.Files {
			if f.Link == "" {
				twig.Infof("API returned no link for %s", f.Name)
				continue
			}
			acc.Files[f.Name] = f
		}
		// finally finished with acc
		accs[acc.ID] = acc
	}
	return accs
}

type Payload struct {
	ID      string `json:"accession,omitempty"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Files   []File `json:"files,omitempty"`
}

type Accession struct {
	ID    string `json:"accession,omitempty"`
	Files map[string]File
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
