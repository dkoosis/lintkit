package check

import (
	"encoding/json"
	"io"
)

// SchemaID is the schema identifier for lintkit-check output.
const SchemaID = "lintkit-check"

// NewReport creates a new report with the schema field set.
func NewReport(tool string) *Report {
	return &Report{
		Schema: SchemaID,
		Tool:   tool,
	}
}

// Write writes the report as JSON to the given writer.
func (r *Report) Write(w io.Writer) error {
	// Derive status if not set
	if r.Status == "" {
		r.Status = r.DeriveStatus()
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// JSON returns the report as a JSON byte slice.
func (r *Report) JSON() ([]byte, error) {
	if r.Status == "" {
		r.Status = r.DeriveStatus()
	}
	return json.MarshalIndent(r, "", "  ")
}

// IsCheck returns true if the given JSON data is a lintkit-check document.
func IsCheck(data []byte) bool {
	var probe struct {
		Schema string `json:"$schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Schema == SchemaID
}

// Parse parses a lintkit-check JSON document.
func Parse(data []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ParseReader parses a lintkit-check JSON document from a reader.
func ParseReader(r io.Reader) (*Report, error) {
	var report Report
	if err := json.NewDecoder(r).Decode(&report); err != nil {
		return nil, err
	}
	return &report, nil
}
