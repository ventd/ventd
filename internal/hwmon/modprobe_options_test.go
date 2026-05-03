package hwmon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsAllowedModprobeOption(t *testing.T) {
	cases := []struct {
		name    string
		module  string
		options string
		want    bool
	}{
		{"thinkpad fan_control=1", "thinkpad_acpi", "fan_control=1", true},
		{"empty module", "", "fan_control=1", false},
		{"empty options", "thinkpad_acpi", "", false},
		{"unknown module", "rogue_module", "fan_control=1", false},
		{"thinkpad with stray space", "thinkpad_acpi", "fan_control=1 ", false},
		{"thinkpad with semicolon", "thinkpad_acpi", "fan_control=1;rm -rf /", false},
		{"thinkpad with disabled value", "thinkpad_acpi", "fan_control=0", false},
		{"future-allowed it87 still rejected today", "it87", "ignore_resource_conflict=1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsAllowedModprobeOption(tc.module, tc.options)
			if got != tc.want {
				t.Errorf("IsAllowedModprobeOption(%q, %q) = %v, want %v",
					tc.module, tc.options, got, tc.want)
			}
		})
	}
}

func TestWriteModprobeOptionsDropIn(t *testing.T) {
	t.Run("creates file with options line", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ventd-thinkpad_acpi.conf")
		if err := WriteModprobeOptionsDropIn(path, "thinkpad_acpi", "fan_control=1"); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		want := "options thinkpad_acpi fan_control=1\n"
		if string(got) != want {
			t.Errorf("file content = %q, want %q", string(got), want)
		}
	})

	t.Run("idempotent on identical content", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ventd-thinkpad_acpi.conf")
		if err := WriteModprobeOptionsDropIn(path, "thinkpad_acpi", "fan_control=1"); err != nil {
			t.Fatalf("first write: %v", err)
		}
		stat1, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat1: %v", err)
		}
		// Sleep micros so mtime would differ if a rewrite happened.
		if err := WriteModprobeOptionsDropIn(path, "thinkpad_acpi", "fan_control=1"); err != nil {
			t.Fatalf("second write: %v", err)
		}
		stat2, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat2: %v", err)
		}
		if !stat1.ModTime().Equal(stat2.ModTime()) {
			t.Errorf("file rewritten on identical-content second write (mtime moved)")
		}
	})

	t.Run("rewrites when options change", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ventd-thinkpad_acpi.conf")
		if err := WriteModprobeOptionsDropIn(path, "thinkpad_acpi", "fan_control=1"); err != nil {
			t.Fatalf("first write: %v", err)
		}
		if err := WriteModprobeOptionsDropIn(path, "thinkpad_acpi", "fan_control=1 experimental=1"); err != nil {
			t.Fatalf("second write: %v", err)
		}
		got, _ := os.ReadFile(path)
		want := "options thinkpad_acpi fan_control=1 experimental=1\n"
		if string(got) != want {
			t.Errorf("after rewrite = %q, want %q", string(got), want)
		}
	})

	t.Run("empty module is an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "x.conf")
		if err := WriteModprobeOptionsDropIn(path, "", "fan_control=1"); err == nil {
			t.Errorf("expected error on empty module")
		}
	})

	t.Run("empty options is an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "x.conf")
		if err := WriteModprobeOptionsDropIn(path, "thinkpad_acpi", ""); err == nil {
			t.Errorf("expected error on empty options")
		}
	})
}

func TestModprobeOptionsDropInPath(t *testing.T) {
	got := ModprobeOptionsDropInPath("thinkpad_acpi")
	want := "/etc/modprobe.d/ventd-thinkpad_acpi.conf"
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
