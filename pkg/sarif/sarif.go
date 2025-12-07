// Package sarif provides types and helpers for emitting SARIF output.
package sarif

import (
	"encoding/json"
	"io"
)

// Version is the SARIF schema version.
const Version = "2.1.0"

// Log is the top-level SARIF structure.
type Log struct {
	Version string `json:"version"`
	Schema  string `json:"$schema,omitempty"`
	Runs    []Run  `json:"runs"`
}

// Run represents a single analysis run.
type Run struct {
	Tool    Tool     `json:"tool"`
	Results []Result `json:"results,omitempty"`
}

// Tool describes the analysis tool.
type Tool struct {
	Driver Driver `json:"driver"`
}

// Driver describes the tool's identity.
type Driver struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`
	InformationURI string `json:"informationUri,omitempty"`
}

// Result is a single finding.
type Result struct {
	RuleID    string     `json:"ruleId"`
	Level     string     `json:"level,omitempty"` // error, warning, note
	Message   Message    `json:"message"`
	Locations []Location `json:"locations,omitempty"`
}

// Message contains the finding's text.
type Message struct {
	Text string `json:"text"`
}

// Location describes where a result was found.
type Location struct {
	PhysicalLocation PhysicalLocation `json:"physicalLocation"`
}

// PhysicalLocation describes a file location.
type PhysicalLocation struct {
	ArtifactLocation ArtifactLocation `json:"artifactLocation"`
	Region           *Region          `json:"region,omitempty"`
}

// ArtifactLocation describes a file path.
type ArtifactLocation struct {
	URI string `json:"uri"`
}

// Region describes a span within a file.
type Region struct {
	StartLine   int `json:"startLine,omitempty"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

// NewLog creates a new SARIF log with default values.
func NewLog() *Log {
	return &Log{
		Version: Version,
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs:    []Run{},
	}
}

// Encoder wraps a JSON encoder with SARIF-friendly defaults.
type Encoder struct {
	enc *json.Encoder
}

// NewEncoder creates an indented JSON encoder for SARIF logs.
func NewEncoder(w io.Writer) *Encoder {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return &Encoder{enc: enc}
}

// Encode writes the SARIF log.
func (e *Encoder) Encode(log *Log) error {
	return e.enc.Encode(log)
}
