package main

import "testing"

// TestStrippedModuleName covers the compressed-kernel-module suffix
// peel used by guessFactoryResetModule's factory-reset OOT-driver
// detection. The v1.0.2 / v1.0.3 implementation matched only bare
// .ko, which silently skipped every Fedora (.ko.xz), Arch (.ko.zst),
// and many Debian/Ubuntu modules under /lib/modules/<rel>/extra/.
func TestStrippedModuleName(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"it87.ko", "it87"},
		{"it87.ko.xz", "it87"},
		{"dell-smm-hwmon.ko.xz", "dell-smm-hwmon"},
		{"nct6687.ko.zst", "nct6687"},
		{"legacy.ko.gz", "legacy"},
		// Non-module entries must return empty so the caller skips them.
		{"README", ""},
		{"some.txt", ""},
		{"weird.xz", ""},
		// Edge: bare .ko after stripping should still match.
		{"x.ko", "x"},
	}
	for _, c := range cases {
		got := strippedModuleName(c.filename)
		if got != c.want {
			t.Errorf("strippedModuleName(%q) = %q, want %q", c.filename, got, c.want)
		}
	}
}
