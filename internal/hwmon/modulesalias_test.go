package hwmon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- parseModulesAlias -------------------------------------------------------

func TestParseModulesAlias(t *testing.T) {
	tests := []struct {
		name  string
		input string
		// wantModules is the set of module names expected as map keys.
		// wantAliases maps a module to one alias it must have (spot-check).
		wantModules  []string
		wantAliases  map[string]string // module → one expected alias substring
		wantExcluded []string          // module names that must NOT appear
	}{
		{
			name:        "empty input yields empty map",
			input:       "",
			wantModules: nil,
		},
		{
			name:        "comment-only input yields empty map",
			input:       "# just a comment\n# another comment\n",
			wantModules: nil,
		},
		{
			name: "single platform alias",
			input: "alias platform:coretemp coretemp\n",
			wantModules: []string{"coretemp"},
			wantAliases: map[string]string{"coretemp": "platform:coretemp"},
		},
		{
			name: "single pci alias",
			input: "alias pci:v00001022d00001603sv*sd*bc*sc*i* k10temp\n",
			wantModules: []string{"k10temp"},
			wantAliases: map[string]string{"k10temp": "pci:"},
		},
		{
			name: "multiple modules and aliases",
			input: "alias platform:coretemp coretemp\n" +
				"alias platform:nct6775 nct6775\n" +
				"alias platform:nct6793 nct6775\n" +
				"alias i2c:adm1021 adm1021\n",
			wantModules: []string{"coretemp", "nct6775", "adm1021"},
			wantAliases: map[string]string{
				"nct6775": "platform:nct6793",
			},
		},
		{
			name: "comments and blank lines are skipped",
			input: "# header\n" +
				"\n" +
				"alias platform:coretemp coretemp\n" +
				"\n" +
				"# another comment\n" +
				"alias platform:it87 it87\n",
			wantModules: []string{"coretemp", "it87"},
		},
		{
			name:         "malformed line (wrong field count) is skipped",
			input:        "notanalias foo\nalias platform:coretemp coretemp\n",
			wantModules:  []string{"coretemp"},
			wantExcluded: []string{"foo", "notanalias"},
		},
		{
			name:         "line starting with 'alias' but only 2 fields is skipped",
			input:        "alias platform:coretemp\nalias platform:it87 it87\n",
			wantModules:  []string{"it87"},
			wantExcluded: []string{"platform:coretemp"},
		},
		{
			name: "same module appears multiple times — aliases accumulate",
			input: "alias platform:nct6775 nct6775\n" +
				"alias platform:nct6793 nct6775\n" +
				"alias platform:nct6798 nct6775\n",
			wantModules: []string{"nct6775"},
			wantAliases: map[string]string{"nct6775": "platform:nct6798"},
		},
		{
			name: "ubuntu2404 fixture: platform modules present, i2c modules present",
			input: func() string {
				data, err := os.ReadFile(filepath.Join("testdata", "modules-alias", "ubuntu2404.txt"))
				if err != nil {
					return ""
				}
				return string(data)
			}(),
			wantModules: []string{"coretemp", "nct6775", "it87"},
			wantAliases: map[string]string{
				"nct6775": "platform:nct6793",
				"adm1021": "i2c:adm1021",
			},
		},
		{
			name: "fedora41 fixture: pci aliases land under correct modules",
			input: func() string {
				data, err := os.ReadFile(filepath.Join("testdata", "modules-alias", "fedora41.txt"))
				if err != nil {
					return ""
				}
				return string(data)
			}(),
			wantModules: []string{"coretemp", "nct6775", "k10temp"},
			wantAliases: map[string]string{
				"k10temp": "pci:",
			},
		},
		{
			name: "arch fixture: of aliases for pwm-fan and gpio-fan",
			input: func() string {
				data, err := os.ReadFile(filepath.Join("testdata", "modules-alias", "arch.txt"))
				if err != nil {
					return ""
				}
				return string(data)
			}(),
			wantModules: []string{"coretemp", "pwm-fan", "gpio-fan"},
			wantAliases: map[string]string{
				"pwm-fan":  "of:",
				"gpio-fan": "of:N*T*Cgpio-fan",
			},
		},
		{
			name:  "utf-8 characters in comment do not panic",
			input: "# Ångström module: nct6775\nalias platform:nct6775 nct6775\n",
			wantModules: []string{"nct6775"},
		},
		{
			name: "leading and trailing whitespace on alias line is handled",
			input: "  alias platform:coretemp coretemp  \n",
			wantModules: []string{"coretemp"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseModulesAlias(strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("parseModulesAlias error: %v", err)
			}

			for _, mod := range tc.wantModules {
				if _, ok := got[mod]; !ok {
					t.Errorf("expected module %q in result, got keys: %v", mod, mapKeys(got))
				}
			}

			for mod, aliasSub := range tc.wantAliases {
				aliases, ok := got[mod]
				if !ok {
					t.Errorf("module %q missing from result", mod)
					continue
				}
				found := false
				for _, a := range aliases {
					if strings.Contains(a, aliasSub) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("module %q aliases %v: none contain %q", mod, aliases, aliasSub)
				}
			}

			for _, mod := range tc.wantExcluded {
				if _, ok := got[mod]; ok {
					t.Errorf("module %q must not appear in result but did", mod)
				}
			}
		})
	}
}

func mapKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// --- parseModulesBuiltinModinfo ----------------------------------------------

func TestParseModulesBuiltinModinfo(t *testing.T) {
	nul := string([]byte{0})

	tests := []struct {
		name         string
		input        string
		wantModule   string
		wantKey      string
		wantValue    string
		wantModCount int // expected number of distinct modules
	}{
		{
			name:         "empty input yields empty map",
			input:        "",
			wantModCount: 0,
		},
		{
			name:         "NUL-only input yields empty map",
			input:        nul + nul,
			wantModCount: 0,
		},
		{
			name:         "single record: filename field",
			input:        "coretemp.filename=(builtin)" + nul,
			wantModule:   "coretemp",
			wantKey:      "filename",
			wantValue:    "(builtin)",
			wantModCount: 1,
		},
		{
			name: "multiple records for same module",
			input: "coretemp.filename=(builtin)" + nul +
				"coretemp.description=Intel CPU temperature" + nul,
			wantModule:   "coretemp",
			wantKey:      "description",
			wantValue:    "Intel CPU temperature",
			wantModCount: 1,
		},
		{
			name: "multiple distinct modules",
			input: "coretemp.filename=(builtin)" + nul +
				"nct6775.filename=(builtin)" + nul +
				"nct6775.description=Nuvoton NCT677x" + nul,
			wantModCount: 2,
		},
		{
			name: "value with equals sign is preserved intact",
			input: "mymod.param=key=value=extra" + nul,
			wantModule:   "mymod",
			wantKey:      "param",
			wantValue:    "key=value=extra",
			wantModCount: 1,
		},
		{
			name:         "record missing dot is skipped",
			input:        "nodotrecord" + nul + "coretemp.filename=(builtin)" + nul,
			wantModule:   "coretemp",
			wantKey:      "filename",
			wantValue:    "(builtin)",
			wantModCount: 1,
		},
		{
			name:         "record missing equals is skipped",
			input:        "coretemp.noequals" + nul + "nct6775.filename=(builtin)" + nul,
			wantModule:   "nct6775",
			wantKey:      "filename",
			wantValue:    "(builtin)",
			wantModCount: 1,
		},
		{
			name:         "dot after equals is skipped (dot > eq)",
			input:        "badrecord=val.ue" + nul + "ok.key=val" + nul,
			wantModule:   "ok",
			wantKey:      "key",
			wantValue:    "val",
			wantModCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseModulesBuiltinModinfo(strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("parseModulesBuiltinModinfo error: %v", err)
			}
			if len(got) != tc.wantModCount {
				t.Errorf("module count: want %d got %d (modules: %v)", tc.wantModCount, len(got), modInfoKeys(got))
			}
			if tc.wantModule != "" {
				fields, ok := got[tc.wantModule]
				if !ok {
					t.Fatalf("module %q missing from result", tc.wantModule)
				}
				if tc.wantKey != "" {
					if got := fields[tc.wantKey]; got != tc.wantValue {
						t.Errorf("got[%q][%q] = %q, want %q", tc.wantModule, tc.wantKey, got, tc.wantValue)
					}
				}
			}
		})
	}
}

func modInfoKeys(m map[string]map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestParseModulesAliasNoPanic verifies arbitrary byte sequences never panic.
func TestParseModulesAliasNoPanic(t *testing.T) {
	inputs := []string{
		"\x00\xff\xfe",
		"alias\x00platform:coretemp\x00coretemp",
		strings.Repeat("alias platform:mod mod\n", 10000),
		"alias  platform:coretemp  coretemp\n", // extra spaces → 4 fields, skipped
	}
	for _, in := range inputs {
		t.Run("nopanic", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on input %q: %v", in, r)
				}
			}()
			_, _ = parseModulesAlias(strings.NewReader(in))
		})
	}
}

// TestParseModulesBuiltinModinfoNoPanic verifies no panic on pathological input.
func TestParseModulesBuiltinModinfoNoPanic(t *testing.T) {
	inputs := []string{
		"\x00\xff\xfe",
		strings.Repeat("mod.key=val\x00", 10000),
		"",
		"\x00",
	}
	for _, in := range inputs {
		t.Run("nopanic", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on input %q: %v", in, r)
				}
			}()
			_, _ = parseModulesBuiltinModinfo(strings.NewReader(in))
		})
	}
}

// --- loadModulesAlias (integration with modulesRootFor) ----------------------

func TestLoadModulesAlias(t *testing.T) {
	logger := newTestLogger()

	t.Run("missing file returns nil without panic", func(t *testing.T) {
		orig := testModulesRoot
		testModulesRoot = t.TempDir() // empty dir, no modules.alias
		defer func() { testModulesRoot = orig }()

		got := loadModulesAlias(testModulesRoot, logger)
		if got != nil {
			t.Errorf("want nil for missing file, got %v", got)
		}
	})

	t.Run("valid file is parsed and returned", func(t *testing.T) {
		root := t.TempDir()
		content := "alias platform:coretemp coretemp\nalias platform:it87 it87\n"
		if err := os.WriteFile(filepath.Join(root, "modules.alias"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		orig := testModulesRoot
		testModulesRoot = root
		defer func() { testModulesRoot = orig }()

		got := loadModulesAlias(root, logger)
		if got == nil {
			t.Fatal("expected non-nil map")
		}
		if _, ok := got["coretemp"]; !ok {
			t.Error("expected coretemp in parsed map")
		}
		if _, ok := got["it87"]; !ok {
			t.Error("expected it87 in parsed map")
		}
	})
}
