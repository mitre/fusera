package nr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func ResolveNames(loc string, ncg string, accs []string) []Accession {
	url := "https://www.ncbi.nlm.nih.gov/Traces/names_test/"
	acc := strings.Join(accs, ",")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if ncg != "" {
		// handle ncg file
		file, err := os.Open(ncg)
		if err != nil {
			panic("couldn't open ncg file")
		}
		defer file.Close()

		part, err := writer.CreateFormFile("ncg", filepath.Base(ncg))
		if err != nil {
			panic("couldn't create multipart file")
		}
		_, err = io.Copy(part, file)
		if err != nil {
			panic("couldn't copy ncg file to multipart file")
		}

	}
	if err := writer.WriteField("version", "xc-1.0"); err != nil {
		panic("could not write version field to multipart.Writer")
	}
	if err := writer.WriteField("format", "json"); err != nil {
		panic("could not write format field to multipart.Writer")
	}
	if loc != "" {
		if err := writer.WriteField("loc", loc); err != nil {
			panic("could not write loc field to multipart.Writer")
		}
	}
	if acc != "" {
		if err := writer.WriteField("acc", acc); err != nil {
			panic("could not write acc field to multipart.Writer")
		}
	}
	if err := writer.Close(); err != nil {
		panic("could not close multipart.Writer")
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		panic("couldn't create request for Name Resolver API")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic("can't resolve acc names")
	}
	defer res.Body.Close()

	var payload []Accession
	err = json.NewDecoder(res.Body).Decode(&payload)
	if err != nil {
		fmt.Println(err)
		// TODO: should see about printing the body
		panic("failed to decode payload when resolving names")
	}

	return payload
}

type Accession struct {
	ID      string `json:"accession"`
	Status  int    `json:"status"`
	Message string `json:"Message,omitempty"`
	Files   []File `json:"files"`
}

type File struct {
	Name           string   `json:"name"`
	Size           FileSize `json:"size"`
	ModifiedDate   FileDate `json:"date_modification"`
	Md5Hash        string   `json:"md5"`
	ExpirationDate FileDate `json:"expirationDate"`
	Link           string   `json:"link"`
}

type FileDate time.Time

func (f *FileDate) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), "\"")
	t, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		return err
	}
	*f = FileDate(t)
	return nil
}

func (f *FileDate) Time() time.Time {
	return time.Time(*f)
}

type FileSize uint64

func (f *FileSize) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), "\"")
	u, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return err
	}
	*f = FileSize(u)
	return nil
}
