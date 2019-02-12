package flags

import (
	"io/ioutil"
	"os"
	"strings"

	"github.com/mitre/fusera/awsutil"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

var (
	EnvPrefix = "dbgap"
	// Version should be set at compile time to `git describe --tags --abbrev=0`
	Version string

	LocationName  = "location"
	AccessionName = "accession"
	NgcName       = "ngc"
	TokenName     = "token"
	FiletypeName  = "filetype"
	EndpointName  = "endpoint"
	AwsBatchName  = "aws-batch"
	GcpBatchName  = "gcp-batch"
	SilentName    = "silent"
	VerboseName   = "verbose"

	Silent  bool
	Verbose bool

	Location  string
	Accession string
	NgcPath   string
	Tokenpath string
	Filetype  string

	Endpoint                      string
	AwsBatch, AwsDefault          int    = 0, 50
	GcpBatch, GcpDefault          int    = 0, 25
	AwsProfile, AwsProfileDefault string = "", "default"
	GcpProfile, GcpProfileDefault string = "", "gcp"
	Eager                         bool

	LocationMsg   = "Cloud provider and region where files should be located.\nFORMAT: [cloud.region]\nEXAMPLES: [s3.us-east-1 | gs.US]\nNOTE: This can be auto-resolved if running on AWS or GCP.\nEnvironment Variable: [$DBGAP_LOCATION]"
	AccessionMsg  = "A list of accessions to mount or path to accession file.\nEXAMPLES: [\"SRR123,SRR456\" | local/accession/file | https://<bucket>.<region>.s3.amazonaws.com/<accession/file>]\nNOTE: If using an s3 url, the proper aws credentials need to be in place on the machine.\nEnvironment Variable: [$DBGAP_ACCESSION]"
	NgcMsg        = "A path to an ngc file used to authorize access to accessions in dbGaP. If used in tandem with token, the token takes precedence.\nEXAMPLES: [local/ngc/file | https://<bucket>.<region>.s3.amazonaws.com/<ngc/file>]\nNOTE: If using an s3 url, the proper aws credentials need to be in place on the machine.\nEnvironment Variable: [$DBGAP_NGC]"
	TokenMsg      = "A path to one of the various security tokens used to authorize access to accessions in dbGaP.\nEXAMPLES: [local/token/file | https://<bucket>.<region>.s3.amazonaws.com/<token/file>]\nNOTE: If using an s3 url, the proper aws credentials need to be in place on the machine.\nEnvironment Variable: [$DBGAP_TOKEN]"
	FiletypeMsg   = "A list of the only file types to copy.\nEXAMPLES: \"cram,crai,bam,bai\"\nEnvironment Variable: [$DBGAP_FILETYPE]"
	EndpointMsg   = "ADVANCED: Change the endpoint used to communicate with SDL API.\nEnvironment Variable: [$DBGAP_ENDPOINT]"
	AwsBatchMsg   = "ADVANCED: Adjust the amount of accessions put in one request to the SDL API when using an AWS location.\nEnvironment Variable: [$DBGAP_AWS-BATCH]"
	GcpBatchMsg   = "ADVANCED: Adjust the amount of accessions put in one request to the SDL API when using a GCP location.\nEnvironment Variable: [$DBGAP_GCP-BATCH]"
	AwsProfileMsg = "The desired AWS credentials profile in ~/.aws/credentials to use for instances when files require the requester (you) to pay for accessing the file.\nEnvironment Variable: [$DBGAP_AWS-PROFILE]\nNOTE: This account will be charged all cost accrued by accessing these certain files through fusera."
	GcpProfileMsg = "The desired GCP credentials profile in ~/.aws/credentials to use for instances when files require the requester (you) to pay for accessing the file.\nEnvironment Variable: [$DBGAP_GCP-PROFILE]\nNOTE: This account will be charged all cost accrued by accessing these certain files through fusera. These credentials should be in the AWS supported format that Google provides in order to work with their AWS compatible API."
	SilentMsg     = "Fusera prints nothing, most useful for using fusera in scripts."
	VerboseMsg    = "Fusera prints everything, most useful for troubleshooting."
)

// ResolveLocation attempts to resolve the location on GCP and AWS.
// If location cannot be resolved, return error.
func ResolveLocation() (string, error) {
	loc, err := awsutil.ResolveTraditionalLocation()
	if err != nil {
		return "", err
	}
	return loc, nil
}

// FindLocation attempts to resolve the location on GCP and AWS.
// If location cannot be resolved, return error.
func FindLocation() (*awsutil.Platform, error) {
	p, err := awsutil.FindLocation()
	if err != nil {
		return nil, err
	}
	// We still need region information to make requests
	return p, nil
}

// ResolveAccession If a list of comma separated accessions was provided, use it.
// Otherwise, if a path to a cart file was given, deduce whether it's on s3 or local.
// Either way, attempt to read the file and make a map of unique accessions.
func ResolveAccession(acc string) ([]string, error) {
	var accessions = make(map[string]bool)
	if strings.HasPrefix(acc, "http") {
		// we were given a url on s3.
		data, err := awsutil.ReadFile(acc)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open accession list file at: %s", acc)
		}
		acc = string(data)
	}
	if NoFileErrors(acc) {
		data, err := ioutil.ReadFile(acc)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open accession list file at: %s", acc)
		}
		acc = string(data)
	}
	// Now process acc
	aa := strings.FieldsFunc(acc, parseAccessions)
	var empty = true
	for _, a := range aa {
		if a != "" {
			empty = false
			accessions[a] = true
		}
	}
	if empty {
		return nil, errors.New("the input given for accessions resulted in no readable form")
	}

	list := make([]string, 0, len(accessions))
	for k := range accessions {
		list = append(list, k)
	}

	return list, nil
}

func parseAccessions(r rune) bool {
	return r == '\n' || r == '\t' || r == ',' || r == ' '
}

// Deduce whether path is on s3 or local.
// Either way, read all of the file into a byte slice.
func ResolveNgcFile(tokenpath string) (data []byte, err error) {
	if strings.HasPrefix(tokenpath, "http") {
		// we were given a url on s3.
		data, err = awsutil.ReadFile(tokenpath)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open token at: %s", tokenpath)
		}
	} else {
		data, err = ioutil.ReadFile(tokenpath)
		if err != nil {
			return nil, errors.Wrapf(err, "couldn't open token at: %s", tokenpath)
		}
	}
	return
}

func ResolveFileType(filetype string) (map[string]bool, error) {
	uniqTypes := make(map[string]bool)
	types := strings.Split(filetype, ",")
	if len(types) == 1 && types[0] == "" {
		return nil, errors.New("filetype was empty")
	}
	if len(types) > 0 {
		for _, t := range types {
			if t != "" && !uniqTypes[t] {
				uniqTypes[t] = true
			}
		}
		return uniqTypes, nil
	}
	return nil, errors.New("filetype was empty")
}

func NoFileErrors(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

func HavePermissions(path string) bool {
	_, err := os.Stat(path)
	return !os.IsPermission(err)
}

func IsAWS(location string) bool {
	return strings.HasPrefix(location, "s3")
}

func IsGCP(location string) bool {
	return strings.HasPrefix(location, "gs")
}

func ResolveBatch(location string, aws, gcp int) int {
	if IsAWS(location) {
		return aws
	}
	if IsGCP(location) {
		return gcp
	}
	return 10
}

func ResolveString(name string, value *string) {
	if value == nil {
		return
	}
	if viper.IsSet(name) {
		env := viper.GetString(name)
		if env != "" {
			*value = env
		}
	}
}

func ResolveInt(name string, value *int) {
	if value == nil {
		return
	}
	if viper.IsSet(name) {
		env := viper.GetInt(name)
		if env != 0 {
			*value = env
		}
	}
}

func ResolveBool(name string, value *bool) {
	if value == nil {
		return
	}
	if viper.IsSet(name) {
		env := viper.GetBool(name)
		*value = env
	}
}
