package sdl

import (
	"time"

	"github.com/mitre/fusera/fuseralib"

	"github.com/mitre/fusera/info"

	"github.com/pkg/errors"
)

// VersionWrap The JSON object that wraps the SDL API's responses.
type VersionWrap struct {
	Version string       `json:"version,omitempty"`
	Result  []*Accession `json:"result,omitempty"`
}

// Validate VersionWrap
// 1. Expected Version.
// 2. Result isn't empty.
func (v *VersionWrap) Validate() error {
	// TODO: Have users set what version they want to use instead of endpoint.
	// Then SdlVersion can be overwritten and this check can actually be valid.
	// if info.SdlVersion != v.Version {
	// 	return errors.Errorf("Expected version: %s, got version: %s", info.SdlVersion, v.Version)
	// }
	if len(v.Result) == 0 {
		return errors.Errorf("SDL API v%s returned an empty response", info.SdlVersion)
	}
	return nil
}

// Accession The JSON object that the SDL API uses to represent an accession.
type Accession struct {
	ID      string  `json:"bundle,omitempty"`
	Status  int     `json:"status,omitempty"`
	Message string  `json:"msg,omitempty"`
	Files   []*File `json:"files,omitempty"`
}

// Validate Accession
// 1. Accession is one of the ones we asked for.
// 2. Status should be an HTTP 200 OK.
// 3. Files shouldn't be empty.
// 4. It's not a duplicate accession (we should only get one of each accession).
// 5. All Files are valid.
func (a *Accession) Validate(isDup map[string]bool) error {
	if !info.LookUpAccession(a.ID) {
		return errors.Errorf("SDL API v%s returned accession that wasn't requested: %s", info.SdlVersion, a.ID)
	}
	if a.Status != 200 {
		return errors.Errorf("SDL API v%s: %s returned status: %d: %s", info.SdlVersion, a.ID, a.Status, a.Message)
	}
	if len(a.Files) == 0 {
		return errors.Errorf("SDL API v%s returned no files for accession %s", info.SdlVersion, a.ID)
	}
	if isDup[a.ID] {
		return errors.Errorf("SDL API v%s returned a duplicate accession: %s", info.SdlVersion, a.ID)
	}
	isDup[a.ID] = true

	for i := range a.Files {
		err := a.Files[i].Validate()
		if err != nil {
			return err
		}
	}
	return nil
}

// Transfigure Changes the SDL representation of an Accession into the Fusera representation.
func (a *Accession) Transfigure() *fuseralib.Accession {
	ff := mapFiles(a.Files)
	return &fuseralib.Accession{
		ID:    a.ID,
		Files: ff,
	}
}

func mapFiles(ff []*File) map[string]fuseralib.File {
	mf := map[string]fuseralib.File{}
	for i := range ff {
		mf[ff[i].Name] = ff[i].Transfigure()
	}
	return mf
}

// File The JSON object that the SDL API uses to represent a file.
type File struct {
	Name         string     `json:"name,omitempty"`
	Size         uint64     `json:"size,omitempty"`
	Type         string     `json:"type,omitempty"`
	ModifiedDate time.Time  `json:"modificationDate,omitempty"`
	Md5Hash      string     `json:"md5,omitempty"`
	Locations    []Location `json:"locations,omitempty"`
}

// Validate Files
// 1. Files need a name.
// 2. Files need a type.
// 3. Files should have one location.
func (f *File) Validate() error {
	if f.Name == "" {
		return errors.Errorf("SDL API v%s returned a file without a name", info.SdlVersion)
	}
	if f.Type == "" {
		return errors.Errorf("SDL API v%s returned a file without a type", info.SdlVersion)
	}
	if len(f.Locations) > 1 {
		return errors.Errorf("SDL API v%s returned multiple locations for file: %s", info.SdlVersion, f.Name)
	}
	if len(f.Locations) == 0 {
		return errors.Errorf("SDL API v%s returned no locations for file: %s", info.SdlVersion, f.Name)
	}
	err := f.Locations[0].Validate()
	if err != nil {
		return err
	}
	return nil
}

// Transfigure Changes the SDL representation of a File into the Fusera representation.
func (f *File) Transfigure() fuseralib.File {
	newfile := fuseralib.File{
		Name:         f.Name,
		Size:         f.Size,
		Type:         f.Type,
		ModifiedDate: f.ModifiedDate,
		Md5Hash:      f.Md5Hash,
	}
	if len(f.Locations) > 0 {
		l := f.Locations[0]
		newfile.Link = l.Link
		newfile.ExpirationDate = l.ExpirationDate
		newfile.Service = l.Service
		newfile.Region = l.Region
		newfile.Bucket = l.Bucket
		newfile.Key = l.Key
		newfile.CeRequired = l.CeRequired
		newfile.PayRequired = l.PayRequired
	}
	return newfile
}

// Location The JSON object used by the SDL API to represent the location of a file.
type Location struct {
	Link           string    `json:"link,omitempty"`
	Service        string    `json:"service,omitempty"`
	Region         string    `json:"region,omitempty"`
	ExpirationDate time.Time `json:"expirationDate,omitempty"`
	CeRequired     bool      `json:"ceRequired,omitempty"`
	PayRequired    bool      `json:"payRequired,omitempty"`
	Bucket         string    `json:"bucket,omitempty"`
	Key            string    `json:"key,omitempty"`
}

// Validate Location
// 1. Link shouldn't be empty.
// 2. Service shouldn't be empty.
// 3. Region shouldn't be empty.
// 4. If PayRequired is true, there must be a Bucket and Key.
func (l *Location) Validate() error {
	if l.Link == "" {
		return errors.Errorf("SDL API v%s returned a file without a link", info.SdlVersion)
	}
	if l.Service == "" {
		return errors.Errorf("SDL API v%s returned a file without indicating what cloud service it's on", info.SdlVersion)
	}
	if l.Region == "" {
		return errors.Errorf("SDL API v%s returned a file without indicating what region it's in", info.SdlVersion)
	}
	if l.PayRequired {
		if l.Bucket == "" {
			return errors.Errorf("SDL API v%s returned a payRequired file without providing a bucket name", info.SdlVersion)
		}
		if l.Key == "" {
			return errors.Errorf("SDL API v%s returned a payRequired file without providing a key for the bucket", info.SdlVersion)
		}
	}
	return nil
}

type apiError struct {
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}
