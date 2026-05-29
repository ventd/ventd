package hwmon

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReconcileUnmanagedManual is the regression guard for
// RULE-CTRL-RECONCILE-STRANDED: a manual channel ventd doesn't control on a
// chip it does control is handed back to firmware auto; a controlled channel,
// an already-auto channel, and a channel on an unrelated chip are all left
// untouched.
func TestReconcileUnmanagedManual(t *testing.T) {
	root := t.TempDir()
	chip := filepath.Join(root, "hwmon9")  // ventd controls pwm4 here
	other := filepath.Join(root, "hwmon8") // ventd controls nothing here
	for _, d := range []string{chip, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, v string) {
		if err := os.WriteFile(p, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		b, _ := os.ReadFile(p)
		return strings.TrimSpace(string(b))
	}

	write(filepath.Join(chip, "pwm1_enable"), "1")  // stranded manual
	write(filepath.Join(chip, "pwm2_enable"), "2")  // already firmware-auto
	write(filepath.Join(chip, "pwm3_enable"), "1")  // stranded manual
	write(filepath.Join(chip, "pwm4_enable"), "1")  // controlled (acquired)
	write(filepath.Join(other, "pwm1_enable"), "1") // manual on an uncontrolled chip

	controlled := map[string]bool{filepath.Join(chip, "pwm4"): true}
	n := ReconcileUnmanagedManual(controlled, slog.Default())

	if n != 2 {
		t.Errorf("restored = %d, want 2 (chip pwm1 + pwm3 stranded)", n)
	}
	if got := read(filepath.Join(chip, "pwm1_enable")); got != "2" {
		t.Errorf("stranded pwm1 = %q, want 2 (firmware auto)", got)
	}
	if got := read(filepath.Join(chip, "pwm3_enable")); got != "2" {
		t.Errorf("stranded pwm3 = %q, want 2 (firmware auto)", got)
	}
	if got := read(filepath.Join(chip, "pwm4_enable")); got != "1" {
		t.Errorf("controlled pwm4 = %q, want 1 (must stay acquired)", got)
	}
	if got := read(filepath.Join(chip, "pwm2_enable")); got != "2" {
		t.Errorf("already-auto pwm2 = %q, want 2 (untouched)", got)
	}
	if got := read(filepath.Join(other, "pwm1_enable")); got != "1" {
		t.Errorf("uncontrolled chip pwm1 = %q, want 1 — ventd must not touch a chip it doesn't control", got)
	}
}
