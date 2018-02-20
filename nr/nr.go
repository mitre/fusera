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
	"os"
	"path/filepath"

	"github.com/mattrbianchi/twig"
	"github.com/pkg/errors"
)

func ResolveNames(loc string, ncg string, accs []string) ([]Accession, error) {
	url := "https://www.ncbi.nlm.nih.gov/Traces/names_test/names.cgi"
	// url := "http://localhost:8000/"
	// acc := strings.Join(accs, ",")
	// fmt.Println("accs sent to name resolver: ", acc)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if ncg != "" {
		// handle ncg file
		file, err := os.Open(ncg)
		if err != nil {
			return nil, errors.Errorf("couldn't open ncg file at: %s", ncg)
		}
		defer file.Close()

		part, err := writer.CreateFormFile("ncg", filepath.Base(ncg))
		if err != nil {
			return nil, errors.Errorf("couldn't create form file from given ncg file: %s", filepath.Base(ncg))
		}
		_, err = io.Copy(part, file)
		if err != nil {
			return nil, errors.Errorf("couldn't copy given ncg file: %s into multipart file to make request", ncg)
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
		for _, acc := range accs {
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("can't resolve acc names")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("error from Name Resolver API: %s", resp.Status)
	}
	// ct := resp.Header.Get("Content-Type")
	// if ct != "application/json" {
	// 	return nil, errors.Errorf("Name Resolver API gave incorrect Content-Type: %s", ct)
	// }

	// Right now the API returns content type as text/json.
	ct := resp.Header.Get("Content-Type")
	if ct != "text/json" {
		return nil, errors.Errorf("Name Resolver API gave incorrect Content-Type: %s", ct)
	}
	var payload []Accession
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response from Name Resolver API")
	}
	if j, err := json.MarshalIndent(payload, "", "\t"); err != nil {
		twig.Debug("failed to marshal response from Name Resolver API for debug logging")
	} else {
		twig.Debugf("Response from Name Resolver API:\n%s", string(j))
	}

	return payload, nil
}

type Accession struct {
	ID      string `json:"accession"`
	Status  int    `json:"status"`
	Message string `json:"message,omitempty"`
	Files   []File `json:"files"`
}

type File struct {
	Name         string `json:"name"`
	Size         string `json:"size"`
	ModifiedDate string `json:"date_modification"`
	Md5Hash      string `json:"md5"`
	Link         string `json:"link"`
}
