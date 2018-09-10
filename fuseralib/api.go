package fuseralib

// API is an interface that describes the functions fusera needs to set itself up.
type API interface {
	Retrieve(accessions []string) ([]*Accession, error)
	Sign(accession string) (*Accession, error)
}
