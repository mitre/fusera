package fuseralib

type API interface {
	Retrieve(accession string) (*Accession, error)
	RetrieveAll() ([]*Accession, error)
	RetrieveAllInBatch(batch int) ([]*Accession, error)
	Sign(accession string) (*Accession, error)
	SignAll() ([]*Accession, error)
}
