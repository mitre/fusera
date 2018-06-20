package fuseralib

// Signer is an interface that allows Fusera to retrieve
// signed urls for an accession.
type Signer interface {
	//GetMetadata(accessions []string) (map[string]*Accession, error)
	//GetSignedURL(accessions []string) (map[string]*Accession, error)
	Sign(accession string) (*Accession, error)
}
