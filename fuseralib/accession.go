package fuseralib

import "time"

type Accession struct {
	ID       string `json:"accession,omitempty"`
	errorLog string
	Files    map[string]File `json:"files,omitempty"`
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
	Size           uint64    `json:"size,omitempty"`
	Type           string    `json:"type,omitempty"`
	ModifiedDate   time.Time `json:"modificationDate,omitempty"`
	Md5Hash        string    `json:"md5,omitempty"`
	Link           string    `json:"link,omitempty"`
	ExpirationDate time.Time `json:"expirationDate,omitempty"`
	Bucket         string    `json:"bucket,omitempty"`
	Key            string    `json:"key,omitempty"`
	Service        string    `json:"service,omitempty"`
	Region         string    `json:"region,omitempty"`
	PayRequired    bool      `json:"payRequired,omitempty"`
	CeRequired     bool      `json:"ceRequired,omitempty"`
}
