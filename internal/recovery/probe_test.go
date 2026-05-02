package recovery

import (
	"context"
	"testing"
	"testing/fstest"
)

// RULE-WIZARD-RECOVERY-11: vendor-daemon active probe returns the
// matching VendorDaemon name when its systemd unit is active, and
// VendorDaemonNone when no unit matches. Walks vendors in stable
// order (System76 → ASUS → Tuxedo → Slimbook) so the test fixture
// can drive each branch independently.
func TestDetectVendorDaemon(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		active map[string]bool
		want   VendorDaemon
	}{
		{
			name:   "no vendor daemon active",
			active: map[string]bool{},
			want:   VendorDaemonNone,
		},
		{
			name:   "system76-power active",
			active: map[string]bool{"system76-power.service": true},
			want:   VendorDaemonSystem76,
		},
		{
			name:   "system76 scheduler alias",
			active: map[string]bool{"system76-scheduler.service": true},
			want:   VendorDaemonSystem76,
		},
		{
			name:   "asusd active",
			active: map[string]bool{"asusd.service": true},
			want:   VendorDaemonAsusctl,
		},
		{
			name:   "asusctl alias",
			active: map[string]bool{"asusctl.service": true},
			want:   VendorDaemonAsusctl,
		},
		{
			name:   "tccd active",
			active: map[string]bool{"tccd.service": true},
			want:   VendorDaemonTuxedo,
		},
		{
			name:   "tuxedofancontrol alias",
			active: map[string]bool{"tuxedofancontrol.service": true},
			want:   VendorDaemonTuxedo,
		},
		{
			name:   "slimbookbattery active",
			active: map[string]bool{"slimbookbattery.service": true},
			want:   VendorDaemonSlimbook,
		},
		{
			name: "system76 wins ordering when multiple are active",
			active: map[string]bool{
				"system76-power.service": true,
				"asusd.service":          true,
			},
			want: VendorDaemonSystem76,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isActive := func(unit string) bool { return tc.active[unit] }
			got := DetectVendorDaemon(context.Background(), isActive)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// RULE-WIZARD-RECOVERY-11b: ctx cancellation returns VendorDaemonNone
// (conservative default — proceed with normal install rather than
// mis-detect under a timed-out probe).
func TestDetectVendorDaemon_CtxCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// isActive that would return true if reached — verify cancel
	// short-circuits before the call lands.
	isActive := func(unit string) bool { return true }
	got := DetectVendorDaemon(ctx, isActive)
	if got != VendorDaemonNone {
		t.Errorf("cancelled ctx: got %q, want %q", got, VendorDaemonNone)
	}
}

// RULE-WIZARD-RECOVERY-12: NixOS detection fires on either
// /etc/NIXOS (the canonical marker file) or /etc/os-release ID=nixos.
// Both quoted and unquoted ID line forms are accepted. Negative
// case verifies a non-NixOS os-release does not fire.
func TestDetectNixOS(t *testing.T) {
	t.Parallel()
	t.Run("etc/NIXOS marker file", func(t *testing.T) {
		fsys := fstest.MapFS{
			"etc/NIXOS": &fstest.MapFile{Data: []byte("")},
		}
		if !DetectNixOS(fsys) {
			t.Fatalf("expected DetectNixOS to fire on /etc/NIXOS")
		}
	})
	t.Run("os-release ID=nixos unquoted", func(t *testing.T) {
		fsys := fstest.MapFS{
			"etc/os-release": &fstest.MapFile{Data: []byte("NAME=NixOS\nID=nixos\nVERSION=24.05\n")},
		}
		if !DetectNixOS(fsys) {
			t.Fatalf("expected DetectNixOS to fire on ID=nixos")
		}
	})
	t.Run("os-release ID=\"nixos\" quoted", func(t *testing.T) {
		fsys := fstest.MapFS{
			"etc/os-release": &fstest.MapFile{Data: []byte("NAME=\"NixOS\"\nID=\"nixos\"\n")},
		}
		if !DetectNixOS(fsys) {
			t.Fatalf("expected DetectNixOS to fire on ID=\"nixos\"")
		}
	})
	t.Run("non-NixOS os-release", func(t *testing.T) {
		fsys := fstest.MapFS{
			"etc/os-release": &fstest.MapFile{Data: []byte("NAME=Ubuntu\nID=ubuntu\nVERSION_ID=24.04\n")},
		}
		if DetectNixOS(fsys) {
			t.Fatalf("ubuntu os-release should not fire")
		}
	})
	t.Run("empty filesystem", func(t *testing.T) {
		fsys := fstest.MapFS{}
		if DetectNixOS(fsys) {
			t.Fatalf("empty fs should not fire")
		}
	})
	t.Run("substring of nixos must not match", func(t *testing.T) {
		// `ID=nixos-arr` (hypothetical typo) shouldn't match. The
		// strict equality check on the trimmed line value is the
		// load-bearing piece — guards against lazy substring
		// matching that would catch derivatives.
		fsys := fstest.MapFS{
			"etc/os-release": &fstest.MapFile{Data: []byte("ID=nixos-arr\n")},
		}
		if DetectNixOS(fsys) {
			t.Fatalf("ID=nixos-arr should not fire (strict equality only)")
		}
	})
}
