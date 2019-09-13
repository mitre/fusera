package fuseralib

// API Describes the functions fuseralib needs in order to properly interact with the SDL API.
type API interface {
	Retrieve(accession string) (*Accession, error)
	RetrieveAll() ([]*Accession, error)
	Sign(accession string) (*Accession, error)
	SignAll() ([]*Accession, error)
	SignAllInBatch(batch int) ([]*Accession, error)
	AddIdent(link string) (string, error)
}

// FetchAccessions A convenience function to serve the specific behavior of first calling the SDL API on start up.
func FetchAccessions(api API, accessions []string, batch int) ([]*Accession, error) {
	if accessions == nil || len(accessions) == 0 { // We have no accessions, but they might be in the token. Alas, no batching can be done.
		return api.SignAll()
	}
	return api.SignAllInBatch(batch)
}
