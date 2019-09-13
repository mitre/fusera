package info

var (
	// Version should be set at compile time to `git describe --tags --abbrev=0`
	Version string
	// BinaryName should be set on init in order to know what binary is using the flags library.
	BinaryName string
	// SdlVersion The version of SDL to use.
	SdlVersion = "2"

	accMap map[string]bool
)

func init() {
	accMap = map[string]bool{}
}

// LoadAccessionMap Loads the Accession Map for easy lookups of whether an accession is a part of the many the user asked for.
func LoadAccessionMap(aa []string) {
	for i := range aa {
		accMap[aa[i]] = true
	}
}

// LookUpAccession returns true if accession is one of the ones asked for by the user. false otherwise.
func LookUpAccession(a string) bool {
	return accMap[a]
}
