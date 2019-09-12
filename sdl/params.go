package sdl

import (
	"bytes"
	"io"
	"mime/multipart"
	"strings"

	"github.com/mitre/fusera/gps"
	"github.com/pkg/errors"
)

// Param The structure to hold all the various parameters accepted by the SDL API.
type Param struct {
	Acc      []string
	Location gps.Locator
	Ngc      []byte
	// Acceptable values are "aws", "gcp", or "aws,gcp"
	AcceptCharges string
	FileType      map[string]bool
}

// NewParam Returns a Param, a convenient structure to hold all the global setting parameters for the SDL API. Most often to be used when creating a new SDL object.
func NewParam(acc []string, location gps.Locator, ngc []byte, charges string, types map[string]bool) *Param {
	return &Param{
		Acc:           acc,
		Location:      location,
		Ngc:           ngc,
		AcceptCharges: charges,
		FileType:      types,
	}
}

// SetAcceptCharges Sets the accept-charges parameter to the proper value according to what cloud profiles were provided.
func SetAcceptCharges(aws, gcp string) string {
	if aws != "" && gcp != "" {
		return "aws,gcp"
	} else if aws != "" && gcp == "" {
		return "aws"
	} else if aws == "" && gcp != "" {
		return "gcp"
	}
	return ""
}

// FileTypes Returns the map of filetypes as a comma separated string for the SDL API parameter "filetypes".
func (p *Param) FileTypes() string {
	tt := make([]string, 0)
	for k := range p.FileType {
		tt = append(tt, k)
	}
	return strings.Join(tt, ",")
}

// AddGlobals Adds all global parameters to writer for a request to the SDL API.
func (p *Param) AddGlobals(writer *multipart.Writer) (*multipart.Writer, error) {
	if err := p.addLocality(writer); err != nil {
		return nil, err
	}
	if err := p.addLocalityType(writer); err != nil {
		return nil, err
	}
	if err := p.addNgc(writer); err != nil {
		return nil, err
	}
	if err := p.addAcceptCharges(writer); err != nil {
		return nil, err
	}
	if err := p.addFileType(writer); err != nil {
		return nil, err
	}
	return writer, nil
}

func (p *Param) addLocality(writer *multipart.Writer) error {
	locality, err := p.Location.Locality()
	if err != nil {
		return err
	}
	if err := writer.WriteField("locality", locality); err != nil {
		return errors.New("could not write locality field to multipart.Writer")
	}
	return nil
}

func (p *Param) addLocalityType(writer *multipart.Writer) error {
	if err := writer.WriteField("locality-type", p.Location.LocalityType()); err != nil {
		return errors.New("could not write locality-type field to multipart.Writer")
	}
	return nil
}

func (p *Param) addNgc(writer *multipart.Writer) error {
	if p.Ngc != nil {
		part, err := writer.CreateFormFile("ngc", "ngc")
		if err != nil {
			return errors.Wrapf(err, "couldn't create form file for ngc")
		}
		_, err = io.Copy(part, bytes.NewReader(p.Ngc))
		if err != nil {
			return errors.Errorf("couldn't copy ngc contents: %s into multipart file to make request", p.Ngc)
		}
	}
	return nil
}

func (p *Param) addAcceptCharges(writer *multipart.Writer) error {
	if p.AcceptCharges != "" {
		if err := writer.WriteField("accept-charges", p.AcceptCharges); err != nil {
			return errors.New("could not write accept-charges field to multipart.Writer")
		}
	}
	return nil
}

func (p *Param) addFileType(writer *multipart.Writer) error {
	if p.FileType != nil {
		if err := writer.WriteField("filetype", p.FileTypes()); err != nil {
			return errors.New("could not write filetype field to multipart.Writer")
		}
	}
	return nil
}

// toAcc Returns the list of accessions as a comma separated string for the SDL API parameter "acc".
func toAcc(aa []string) string {
	if len(aa) == 1 {
		return aa[0]
	}
	return strings.Join(aa, ",")
}

func addAccessions(writer *multipart.Writer, aa []string) error {
	if err := writer.WriteField("acc", toAcc(aa)); err != nil {
		return errors.New("could not write acc field to multipart.Writer")
	}
	return nil
}

func addMetaOnly(writer *multipart.Writer) error {
	if err := writer.WriteField("meta-only", "yes"); err != nil {
		return errors.New("could not write meta-only field to multipart.Writer")
	}
	return nil
}
