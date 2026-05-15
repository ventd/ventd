package nbfc

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// embedded holds the vendored upstream catalogue. Synced from
// github.com/nbfc-linux/nbfc-linux@0.5.2 — see UPSTREAM for the
// commit SHA + sync date.
//
//go:embed configs/*.json UPSTREAM LICENSE.upstream
var embedded embed.FS

// EmbeddedFS exposes the vendored catalogue as an fs.FS for callers
// who want to LoadCatalog directly without rebuilding their own
// embed declaration. Tests inject their own fs.FS via LoadCatalogFS.
func EmbeddedFS() fs.FS { return embedded }

// Catalog is the parsed in-memory result of LoadCatalogFS over the
// vendored configs/ directory. NotebookModels is indexed by upstream
// NotebookModel string; the value is the parsed Config plus the
// classified ControlMode + source filename for diagnostics.
type Catalog struct {
	Entries []*Entry
	byModel map[string]*Entry
}

// Entry is one catalogue row: the parsed Config + the source filename
// + the classified ControlMode.
type Entry struct {
	Config   *Config
	Filename string
	Mode     ControlMode
}

// Lookup returns the entry for an exact NotebookModel match, or nil.
// Case-folded comparison is the caller's responsibility (see Match).
func (c *Catalog) Lookup(model string) *Entry {
	if c == nil {
		return nil
	}
	return c.byModel[model]
}

// Size returns the number of parsed entries; useful for the doctor
// detector to report "311 known laptops" or similar.
func (c *Catalog) Size() int {
	if c == nil {
		return 0
	}
	return len(c.Entries)
}

// LoadCatalog parses the vendored embedded catalogue. Returns a
// fully-populated Catalog with every NotebookModel keyed, or an
// error naming the first failing file (RULE-NBFC-CATALOG-01:
// malformed config aborts daemon start so the operator sees a
// clear "file X failed to parse" message rather than the silent
// half-catalogue you'd get from skip-on-error).
func LoadCatalog() (*Catalog, error) {
	return LoadCatalogFS(embedded, "configs")
}

// LoadCatalogFS is the test-friendly variant: parses every *.json
// file in the named subdirectory of fsys. Production calls
// LoadCatalog which forwards to this with the embedded FS rooted
// at "configs".
func LoadCatalogFS(fsys fs.FS, dir string) (*Catalog, error) {
	matches, err := fs.Glob(fsys, dir+"/*.json")
	if err != nil {
		return nil, fmt.Errorf("nbfc: glob %s: %w", dir, err)
	}
	cat := &Catalog{
		byModel: make(map[string]*Entry, len(matches)),
	}
	for _, path := range matches {
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("nbfc: read %s: %w", path, err)
		}
		cfg := &Config{}
		// A handful of upstream configs include // line comments and
		// /* block comments */ — accepted by nbfc-linux's C decoder
		// but rejected by Go's strict encoding/json. Strip them
		// (string-aware) before parsing.
		clean := stripJSONComments(raw)
		if err := json.Unmarshal(clean, cfg); err != nil {
			return nil, fmt.Errorf("nbfc: parse %s: %w", path, err)
		}
		if strings.TrimSpace(cfg.NotebookModel) == "" {
			return nil, fmt.Errorf("nbfc: %s: empty NotebookModel", path)
		}
		entry := &Entry{
			Config:   cfg,
			Filename: path,
			Mode:     classifyControlMode(clean, cfg),
		}
		cat.Entries = append(cat.Entries, entry)
		// Last-writer-wins on duplicate NotebookModel; upstream guarantees
		// uniqueness, but be defensive.
		cat.byModel[cfg.NotebookModel] = entry
	}
	sort.Slice(cat.Entries, func(i, j int) bool {
		return cat.Entries[i].Config.NotebookModel < cat.Entries[j].Config.NotebookModel
	})
	return cat, nil
}

// classifyControlMode inspects both the parsed config (typed) and the
// raw JSON (untyped substring check) so a config that introduces a
// Lua or ACPI field via a key our schema doesn't model yet still
// classifies into the right bucket. RULE-NBFC-CATALOG-03 demands
// the classification stay exhaustive across the catalogue.
func classifyControlMode(raw []byte, cfg *Config) ControlMode {
	// Lua takes precedence — any Lua usage means we can't drive this
	// hardware in v0.8.0 regardless of register / ACPI presence.
	rawStr := string(raw)
	if len(cfg.LuaLibraries) > 0 ||
		strings.Contains(rawStr, `"LuaCode"`) ||
		strings.Contains(rawStr, `"ReadLuaCode"`) ||
		strings.Contains(rawStr, `"WriteLuaCode"`) ||
		strings.Contains(rawStr, `"ResetLuaCode"`) {
		return ControlModeLua
	}
	// ACPI: any AcpiMethod field present in the JSON.
	if strings.Contains(rawStr, `"AcpiMethod"`) ||
		strings.Contains(rawStr, `"ReadAcpiMethod"`) ||
		strings.Contains(rawStr, `"WriteAcpiMethod"`) ||
		strings.Contains(rawStr, `"ResetAcpiMethod"`) {
		return ControlModeACPI
	}
	if cfg.ReadWriteWords {
		return ControlModeRegister16
	}
	return ControlModeRegister
}

// stripJSONComments normalises a JSON-with-comments byte stream into
// strict JSON. It removes // line comments and /* block comments */,
// plus any trailing comma immediately preceding a `]` or `}`. A small
// subset of upstream nbfc-linux configs are authored as JSONC (Acer
// Nitro V15-41, Acer Predator PH315-54, Asus ROG G75VX as of v0.5.2);
// the upstream C parser tolerates them, so our Go-side loader must
// too. Trailing commas often appear post-strip when an entire trailing
// array element is commented out (Acer Predator PH315-54's CoolBoost
// stub).
//
// String-aware: anything inside a "..." string literal is passed
// through unchanged. Escapes are tracked so `"\"//\""` survives
// intact. Single-pass state machine — no regex (which can't track
// quote / escape state robustly across multi-line bodies).
func stripJSONComments(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString := false
	escaped := false
	for i := 0; i < len(in); i++ {
		b := in[i]
		if inString {
			out = append(out, b)
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}
		if b == '/' && i+1 < len(in) {
			switch in[i+1] {
			case '/':
				// Skip to end of line, but keep the newline so token
				// positions don't shift wildly in error messages.
				j := i + 2
				for j < len(in) && in[j] != '\n' {
					j++
				}
				i = j - 1 // loop's i++ lands on the newline (or end)
				continue
			case '*':
				j := i + 2
				for j+1 < len(in) && (in[j] != '*' || in[j+1] != '/') {
					j++
				}
				// j+1 is the '/' of '*/', or we walked off the end.
				if j+1 < len(in) {
					i = j + 1
				} else {
					i = len(in) - 1
				}
				continue
			}
		}
		out = append(out, b)
	}
	return stripLeadingZeros(rewriteHexLiterals(stripTrailingCommas(out)))
}

// stripLeadingZeros normalises JSON5-style numeric literals with
// leading zeros (e.g. `00.0`, `007`) into strict-JSON form (`0.0`,
// `7`). String-aware. Runs after rewriteHexLiterals so any hex
// number has already been collapsed to decimal.
func stripLeadingZeros(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString := false
	escaped := false
	for i := 0; i < len(in); i++ {
		b := in[i]
		if inString {
			out = append(out, b)
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}
		// Outside a string. A digit run is the start of a number only
		// if preceded by structural punctuation, whitespace, or BOF.
		// Optional leading `-` is preserved.
		if (b == '0') && (i == 0 || isNumberBoundary(in[i-1])) {
			// Walk forward and consume the digit-run.
			j := i + 1
			for j < len(in) && in[j] == '0' {
				j++
			}
			// Now in[j] is the first non-zero. If it's a digit, the
			// leading zeros were redundant — drop them. If it's '.'
			// or the literal ends, keep a single '0'.
			if j < len(in) && in[j] >= '1' && in[j] <= '9' {
				// Skip the leading zero(s); the next byte will be
				// emitted next iteration.
				i = j - 1
				continue
			}
			// Keep a single 0 for "0", "0.5", "0,", "0}", etc.
			out = append(out, '0')
			i = j - 1
			continue
		}
		out = append(out, b)
	}
	return out
}

// isNumberBoundary returns true when the preceding byte permits the
// next byte to start a JSON number literal. Structural punctuation
// (`:`, `,`, `[`, ` `, newline, tab) qualifies; anything alphanumeric
// or underscore-ish does not (we don't want to rewrite identifiers
// inside descriptions — which live in strings anyway, but defence in
// depth never hurt).
func isNumberBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', ':', ',', '[':
		return true
	}
	return false
}

// rewriteHexLiterals rewrites JSON5-style `0xNN` hex number literals to
// their decimal equivalents (which strict JSON accepts). 8 upstream
// configs as of v0.5.2 author register addresses as 0x17 / 0x18 etc.
// String-aware: hex sequences inside string literals are preserved.
// Walk the bytes; outside a string, when we see `0x` followed by ≥1
// hex digit, parse the run and emit the decimal form.
func rewriteHexLiterals(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString := false
	escaped := false
	for i := 0; i < len(in); i++ {
		b := in[i]
		if inString {
			out = append(out, b)
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}
		// Look for 0x or 0X followed by ≥1 hex digit. The literal must
		// stand alone (preceded by non-alphanumeric); otherwise we'd
		// rewrite "ignore_0xff" identifiers. JSON outside strings only
		// contains structural punctuation, whitespace, and numbers — so
		// any preceding char that's neither alnum nor `_` is fine.
		if b == '0' && i+1 < len(in) && (in[i+1] == 'x' || in[i+1] == 'X') {
			prevOK := i == 0 || !isJSONNumOrIdent(in[i-1])
			if prevOK {
				j := i + 2
				for j < len(in) && isHexDigit(in[j]) {
					j++
				}
				if j > i+2 {
					// Parse the hex digits.
					var n uint64
					for k := i + 2; k < j; k++ {
						n = n*16 + uint64(hexVal(in[k]))
					}
					out = append(out, []byte(formatUint(n))...)
					i = j - 1
					continue
				}
			}
		}
		out = append(out, b)
	}
	return out
}

func isJSONNumOrIdent(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		b == '_'
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'f') ||
		(b >= 'A' && b <= 'F')
}

func hexVal(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0
}

func formatUint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// stripTrailingCommas removes any `,` that appears immediately before a
// closing `]` or `}` (with whitespace allowed between). String-aware —
// commas inside string literals are preserved. The pass runs after
// stripJSONComments so commented-out trailing array elements that left
// a dangling `,` get normalised cleanly.
func stripTrailingCommas(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString := false
	escaped := false
	for i := 0; i < len(in); i++ {
		b := in[i]
		if inString {
			out = append(out, b)
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}
		if b == ',' {
			// Look ahead for whitespace then `]` or `}`.
			j := i + 1
			for j < len(in) {
				switch in[j] {
				case ' ', '\t', '\r', '\n':
					j++
					continue
				case ']', '}':
					// Skip this comma — emit the closer + everything
					// between via outer loop continuation.
					i = j - 1
					goto nextByte
				}
				break
			}
		}
		out = append(out, b)
	nextByte:
	}
	return out
}

// rawStringOrArray decodes a JSON value that the upstream nbfc-linux
// schema permits as either a single string or an array of strings.
// We don't execute Lua in Phase A; the type's only job is to not
// crash on either shape. Phase B3's Lua-refusal already rejects
// configs containing this field non-empty.
type rawStringOrArray []string

func (r *rawStringOrArray) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*r = nil
		return nil
	}
	// Array form.
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return fmt.Errorf("nbfc: rawStringOrArray (array): %w", err)
		}
		*r = arr
		return nil
	}
	// String form.
	var single string
	if err := json.Unmarshal(data, &single); err != nil {
		return fmt.Errorf("nbfc: rawStringOrArray (string): %w", err)
	}
	*r = []string{single}
	return nil
}

// IsEmpty reports whether any string content is present.
func (r rawStringOrArray) IsEmpty() bool {
	for _, s := range r {
		if strings.TrimSpace(s) != "" {
			return false
		}
	}
	return true
}
