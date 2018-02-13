package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Payload struct {
	Accessions []Accession `json:"accessions"`
}

type Accession struct {
	ID           string `json:"accession"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Files        []File `json:"files"`
}

type File struct {
	Name      string    `json:"name"`
	Size      uint64    `json:"size"`
	Date      time.Time `json:"date"`
	Type      string    `json:"type"`
	Md5Hash   string    `json:"md5"`
	SignedUrl SignedUrl `json:"signedUrl"`
}

type SignedUrl struct {
	ExpirationDate time.Time `json:"expirationDate"`
	Region         string    `json:"region"`
	Link           string    `json:"link"`
}

func resolveNames(loc string, ncg string, acc []string) Payload {
	url := "http://localhost:8000/"
	accString := strings.Join(acc, ",")
	q := fmt.Sprintf("version=xc-1.0&format=json&location=%s&ncg=%s&acc=%s", loc, ncg, accString)
	query := strings.NewReader(q)
	req, _ := http.NewRequest("POST", url, query)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic("can't resolve acc names")
	}
	defer res.Body.Close()

	var payload Payload
	err = json.NewDecoder(res.Body).Decode(&payload)
	if err != nil {
		panic("failed to decode payload when resolving names")
	}

	return payload
}

func getBucketName(p Payload) string {
	for _, acc := range p.Accessions {
		for _, file := range acc.Files {
			return file.SignedUrl.Link
		}
	}
	return ""
}
