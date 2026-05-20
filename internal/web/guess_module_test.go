package web

import "testing"

// TestStrippedKernelModuleName mirrors the cmd/ventd factory-reset
// helper test against the web-package twin. Both helpers feed the
// /lib/modules/<rel>/extra/ walk that backs reset-and-reinstall and
// factory reset; before v1.0.4 both matched only bare .ko, which is
// rare in distro kernels.
func TestStrippedKernelModuleName(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"it87.ko", "it87"},
		{"it87.ko.xz", "it87"},
		{"dell-smm-hwmon.ko.xz", "dell-smm-hwmon"},
		{"nct6687.ko.zst", "nct6687"},
		{"legacy.ko.gz", "legacy"},
		{"README", ""},
		{"some.txt", ""},
		{"weird.xz", ""},
		{"x.ko", "x"},
	}
	for _, c := range cases {
		got := strippedKernelModuleName(c.filename)
		if got != c.want {
			t.Errorf("strippedKernelModuleName(%q) = %q, want %q", c.filename, got, c.want)
		}
	}
}
