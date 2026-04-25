package diag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ManifestFile describes one file in the bundle.
type ManifestFile struct {
	Schema    string `json:"schema,omitempty"`
	SizeBytes int    `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// Manifest is the machine-readable index written as manifest.json.
type Manifest struct {
	SchemaVersion  string                  `json:"schema_version"`
	VentdVersion   string                  `json:"ventd_version"`
	CapturedAt     time.Time               `json:"captured_at"`
	HostIDRedacted string                  `json:"host_id_redacted"`
	Files          map[string]ManifestFile `json:"files"`
	MissingTools   []string                `json:"missing_tools,omitempty"`
}

// NewManifest creates an empty manifest.
func NewManifest(ventdVersion string) *Manifest {
	return &Manifest{
		SchemaVersion: "ventd-diag-bundle-v1",
		VentdVersion:  ventdVersion,
		CapturedAt:    time.Now().UTC(),
		Files:         make(map[string]ManifestFile),
	}
}

// AddFile records a file in the manifest with its sha256 and size.
func (m *Manifest) AddFile(path string, content []byte, schema string) {
	sum := sha256.Sum256(content)
	m.Files[path] = ManifestFile{
		Schema:    schema,
		SizeBytes: len(content),
		SHA256:    hex.EncodeToString(sum[:]),
	}
}

// Marshal serialises the manifest to indented JSON.
func (m *Manifest) Marshal() ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("manifest marshal: %w", err)
	}
	return data, nil
}
