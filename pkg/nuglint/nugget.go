// Package nuglint provides linting for ORCA knowledge nugget JSONL files.
package nuglint

// Nug represents the minimal structure of a nugget entry.
type Nug struct {
	ID        string   `json:"id"`
	Kind      string   `json:"k"`
	Rationale string   `json:"r"`
	Tags      []string `json:"tags"`
	HasSev    bool     `json:"-"`
}
