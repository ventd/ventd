// SPDX-License-Identifier: GPL-3.0-or-later
package lmsensors

import (
	"bytes"
	"strings"
	"testing"
)

const sampleConf = `# Sample sensors.conf for testing
# Mimics a fragment of upstream sensors.conf.d/asus.conf

chip "it87-*" "nct6775-*"
    label in0 "Vcore"
    label in1 "+12V"
    label fan1 "CPU Fan"
    label temp1 "CPU"
    ignore in7

chip "k10temp-*"
    label temp1 "Ryzen CCD Tdie"

# A chip with neither labels nor ignores — should be dropped from the overlay.
chip "drivetemp-*"
`

func TestParse_ChipBlocksWithLabelsAndIgnores(t *testing.T) {
	blocks, err := Parse(strings.NewReader(sampleConf))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("got %d chip blocks, want 3", len(blocks))
	}

	// First block: two chip patterns, four labels, one ignore.
	b0 := blocks[0]
	if len(b0.Patterns) != 2 {
		t.Errorf("block 0: %d patterns, want 2", len(b0.Patterns))
	}
	if b0.Patterns[0] != "it87-*" || b0.Patterns[1] != "nct6775-*" {
		t.Errorf("block 0 patterns = %v, want [it87-*, nct6775-*]", b0.Patterns)
	}
	if b0.Labels["in0"] != "Vcore" {
		t.Errorf("block 0: in0 label = %q, want Vcore", b0.Labels["in0"])
	}
	if b0.Labels["fan1"] != "CPU Fan" {
		t.Errorf("block 0: fan1 label = %q, want \"CPU Fan\"", b0.Labels["fan1"])
	}
	if _, ok := b0.Ignored["in7"]; !ok {
		t.Errorf("block 0: in7 not in Ignored set")
	}

	// Third block: empty (drivetemp-* has neither labels nor ignores).
	b2 := blocks[2]
	if len(b2.Labels) != 0 || len(b2.Ignored) != 0 {
		t.Errorf("block 2: expected empty, got labels=%v ignores=%v", b2.Labels, b2.Ignored)
	}
}

func TestParse_HandlesUnterminatedQuoteAsError(t *testing.T) {
	bad := `chip "nct6775-*"
    label in0 "Vcore`
	_, err := Parse(strings.NewReader(bad))
	if err == nil {
		t.Errorf("expected error on unterminated quoted string, got nil")
	}
}

func TestParse_IgnoresCommentsAndBlankLines(t *testing.T) {
	in := `
# top-of-file comment
   # indented comment

chip "nct6775-*"
    # in-block comment
    label in0 "Vcore"  # trailing comment
`
	blocks, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Labels["in0"] != "Vcore" {
		t.Errorf("comment stripping broke; got blocks=%+v", blocks)
	}
}

func TestParse_UnknownDirectivesSilentlySkipped(t *testing.T) {
	in := `chip "nct6775-*"
    compute in0 @ * 2, @ / 2
    set in0 1.0
    bus "i2c-0" "SMBus I801 adapter"
    label fan1 "CPU"
`
	blocks, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Labels["fan1"] != "CPU" {
		t.Errorf("unknown-directive skip ate the subsequent label; got %+v", blocks[0])
	}
}

func TestStripComment_PreservesHashInsideQuote(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"label x \"foo#bar\" # tail", "label x \"foo#bar\" "},
		{"# everything dropped", ""},
		{"chip \"nct6775-*\"", "chip \"nct6775-*\""},
		{"", ""},
	}
	for _, tt := range tests {
		got := stripComment(tt.in)
		if got != tt.want {
			t.Errorf("stripComment(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEmitOverlay_DropsEmptyBlocks(t *testing.T) {
	blocks, _ := Parse(strings.NewReader(sampleConf))
	var buf bytes.Buffer
	if err := EmitOverlay(&buf, "/etc/sensors3.conf", blocks); err != nil {
		t.Fatalf("EmitOverlay: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `schema_version: "1.3"`) {
		t.Errorf("overlay missing schema_version line\n---\n%s\n---", out)
	}
	// chip_overlays for the it87/nct6775 block must appear.
	if !strings.Contains(out, "it87-*") || !strings.Contains(out, "nct6775-*") {
		t.Errorf("overlay missing the it87/nct6775 patterns\n---\n%s\n---", out)
	}
	// drivetemp-* had no labels/ignores; it must NOT appear.
	if strings.Contains(out, "drivetemp-*") {
		t.Errorf("overlay erroneously included empty drivetemp block\n---\n%s\n---", out)
	}
	// k10temp's lone label must appear.
	if !strings.Contains(out, `temp1: "Ryzen CCD Tdie"`) {
		t.Errorf("overlay missing k10temp label\n---\n%s\n---", out)
	}
	// in7 ignore for the first block must appear.
	if !strings.Contains(out, "- in7") {
		t.Errorf("overlay missing in7 ignore\n---\n%s\n---", out)
	}
}

func TestEmitOverlay_DeterministicAcrossRuns(t *testing.T) {
	blocks, _ := Parse(strings.NewReader(sampleConf))
	var a, b bytes.Buffer
	_ = EmitOverlay(&a, "/etc/sensors3.conf", blocks)
	_ = EmitOverlay(&b, "/etc/sensors3.conf", blocks)
	if a.String() != b.String() {
		t.Errorf("EmitOverlay non-deterministic — two runs produced different output")
	}
}

func TestEmitOverlay_NoChipBlocksProducesCommentOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := EmitOverlay(&buf, "/dev/null", nil); err != nil {
		t.Fatalf("EmitOverlay: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "chip_overlays:") {
		t.Errorf("empty input emitted chip_overlays: header\n---\n%s\n---", out)
	}
	if !strings.Contains(out, "no chip blocks") {
		t.Errorf("empty-input output should carry a comment explaining the empty result\n---\n%s\n---", out)
	}
}
