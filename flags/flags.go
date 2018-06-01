package flags

import (
	"io/ioutil"
	"os"
	"strings"

	"github.com/mitre/fusera/awsutil"
	"github.com/pkg/errors"
)

// Attempt to resolve the location on GCP and AWS.
// If location cannot be resolved, return error.
func ResolveLocation() (string, error) {
	loc, err := awsutil.ResolveRegion()
	if err != nil {
		return "", err
	}
	return loc, nil
}

// If a list of comma separated accessions was provided, use it.
// Otherwise, if a path to a cart file was given, deduce whether it's on s3 or local.
// Either way, attempt to read the file and make a map of unique accessions.
func ResolveAccession(acc string) (map[string]bool, error) {
	var accessions = make(map[string]bool)
	if strings.HasPrefix(acc, "http") {
		// we were given a url on s3.
		data, err := awsutil.ReadFile(acc)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open cart file at: %s", acc)
		}
		acc = string(data)
	} else if FileExists(acc) {
		data, err := ioutil.ReadFile(acc)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open cart file at: %s", acc)
		}
		acc = string(data)
	}
	// Now process acc
	aa := strings.FieldsFunc(acc, ParseAccessions)
	var empty = true
	for _, a := range aa {
		if a != "" {
			empty = false
			accessions[a] = true
		}
	}
	if empty {
		return nil, errors.Errorf("No accessions were found in the content given to the --accession flag. --accession: %s.", acc)
	}

	return accessions, nil
}

func ParseAccessions(r rune) bool {
	return r == '\n' || r == '\t' || r == ',' || r == ' '
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// Deduce whether path is on s3 or local.
// Either way, read all of the file into a byte slice.
func ResolveNgcFile(ngcpath string) (data []byte, err error) {
	// we were given a path to an ngc file. Let's read it.
	if strings.HasPrefix(ngcpath, "http") {
		// we were given a url on s3.
		data, err = awsutil.ReadFile(ngcpath)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open ngc file at: %s", ngcpath)
		}
	} else {
		data, err = ioutil.ReadFile(ngcpath)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open ngc file at: %s", ngcpath)
		}
	}
	return
}

func ResolveFileType(filetype string) (map[string]bool, error) {
	uniqTypes := make(map[string]bool)
	types := strings.Split(filetype, ",")
	if len(types) == 1 && types[0] == "" {
		return nil, errors.New("")
	}
	if len(types) > 0 {
		for _, t := range types {
			if t != "" && !uniqTypes[t] {
				uniqTypes[t] = true
			}
		}
		return uniqTypes, nil
	}
	return nil, errors.New("")
}
