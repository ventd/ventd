// Copyright the ventd authors.
// SPDX-License-Identifier: GPL-3.0-or-later

package probe

import (
	"io/fs"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

// TestClassifyBaseline pins the per-channel verdict rules:
//
//   - any non-zero sample → real
//   - all-zero AND no paired PWM → phantom
//   - all-zero WITH paired PWM → real (polarity probe owns the downgrade)
//   - empty samples → phantom (degenerate)
func TestClassifyBaseline(t *testing.T) {
	cases := []struct {
		name      string
		samples   []int
		pairedPWM string
		want      MonitorChannelVisibility
	}{
		{"non-zero rpm → real", []int{1200, 1205, 1210, 1198, 1200}, "", VisibilityReal},
		{"single non-zero → real", []int{0, 0, 50, 0, 0}, "", VisibilityReal},
		{"all-zero no pwm → phantom", []int{0, 0, 0, 0, 0}, "", VisibilityPhantom},
		{"all-zero with pwm → real (polarity owns)", []int{0, 0, 0, 0, 0}, "/sys/hwmon0/pwm2", VisibilityReal},
		{"empty samples → phantom", nil, "", VisibilityPhantom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBaseline(tc.samples, tc.pairedPWM); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestSamplesAreMirror verifies the ±MirrorEpsilonRPM tolerance for
// the mirror classifier.
func TestSamplesAreMirror(t *testing.T) {
	cases := []struct {
		name string
		a, b []int
		want bool
	}{
		{"identical → mirror", []int{1200, 1205, 1210}, []int{1200, 1205, 1210}, true},
		{"within epsilon → mirror", []int{1200, 1205, 1210}, []int{1203, 1208, 1213}, true}, // diffs 3,3,3
		{"one sample over epsilon → not mirror", []int{1200, 1205, 1210}, []int{1200, 1205, 1230}, false},
		{"opposite signs within epsilon → mirror", []int{1200, 1205, 1210}, []int{1196, 1208, 1213}, true},
		{"empty → not mirror", []int{}, []int{}, false},
		{"length mismatch → not mirror", []int{1200}, []int{1200, 1200}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := samplesAreMirror(tc.a, tc.b); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

// TestIsFanInputFile pins the file-name validator.
func TestIsFanInputFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"fan1_input", true},
		{"fan10_input", true},
		{"fan1_label", false},
		{"fan_input", false},
		{"fanA_input", false},
		{"pwm1", false},
		{"temp1_input", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFanInputFile(tc.name); got != tc.want {
				t.Errorf("isFanInputFile(%q) = %v want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestEnumerateMonitorChannels_ClassifiesMinipcShape covers the
// issue's central repro: one chip with four `fan*_input` zones where
// one is real, two are mirrors of the real one, and one is a phantom
// EC-reported zone with no fan wired. The classifier must:
//
//  1. mark fan1 as real (RPM > 0, unique baseline)
//  2. mark fan2 + fan3 as mirror of fan1 (RPM tracks fan1 within ±5)
//  3. mark fan4 as phantom (all-zero RPM, no paired PWM)
func TestEnumerateMonitorChannels_ClassifiesMinipcShape(t *testing.T) {
	// Map RPM-per-sample-step by tach path. The reader callback
	// produces a deterministic baseline stream without touching sysfs.
	const (
		fan1Path = "/sys/class/hwmon/hwmon3/fan1_input"
		fan2Path = "/sys/class/hwmon/hwmon3/fan2_input"
		fan3Path = "/sys/class/hwmon/hwmon3/fan3_input"
		fan4Path = "/sys/class/hwmon/hwmon3/fan4_input"
	)
	rpmStreams := map[string][]int{
		fan1Path: {1200, 1205, 1210, 1198, 1200},
		fan2Path: {1202, 1206, 1208, 1199, 1201}, // mirror
		fan3Path: {1200, 1204, 1210, 1198, 1200}, // mirror
		fan4Path: {0, 0, 0, 0, 0},                // phantom
	}
	// Track per-path call index so each sample reads the next slot.
	perPathIdx := map[string]*atomic.Int64{
		fan1Path: {}, fan2Path: {}, fan3Path: {}, fan4Path: {},
	}
	read := func(path string) (int, error) {
		idx := perPathIdx[path]
		if idx == nil {
			return 0, nil
		}
		i := idx.Add(1) - 1
		stream := rpmStreams[path]
		if len(stream) == 0 {
			return 0, nil
		}
		return stream[int(i)%len(stream)], nil
	}

	// fstest.MapFS keyed under "class/hwmon" because EnumerateMonitorChannels
	// expects sysFS rooted at /sys (same convention as ProbeConfig.SysFS)
	// and hwmonRoot relative to that ("class/hwmon"). Tach paths come back
	// as "/sys/class/hwmon/hwmon3/fanN_input".
	sysFS := fstest.MapFS{
		"class/hwmon/hwmon3":            {Mode: fs.ModeDir | 0o755},
		"class/hwmon/hwmon3/name":       {Data: []byte("nct6687\n")},
		"class/hwmon/hwmon3/fan1_input": {Data: []byte("1200\n")},
		"class/hwmon/hwmon3/fan2_input": {Data: []byte("1202\n")},
		"class/hwmon/hwmon3/fan3_input": {Data: []byte("1200\n")},
		"class/hwmon/hwmon3/fan4_input": {Data: []byte("0\n")},
	}
	_ = filepath.Join // silence unused-import linter if path helper unused
	pairedPWMs := map[string]string{
		fan1Path: "/sys/class/hwmon/hwmon3/pwm1",
	}

	t.Run("classifies minipc shape", func(t *testing.T) {
		// 500ms baseline window is acceptable here.
		start := time.Now()
		got := EnumerateMonitorChannels(sysFS, "class/hwmon", read, pairedPWMs)
		elapsed := time.Since(start)
		if elapsed > 2*time.Second {
			t.Errorf("classifier took %s, expected <2s", elapsed)
		}
		if len(got) != 4 {
			t.Fatalf("enumerated %d channels, want 4: %+v", len(got), got)
		}
		visByPath := map[string]MonitorChannelVisibility{}
		mirrorOf := map[string]string{}
		for _, ch := range got {
			visByPath[ch.TachPath] = ch.Visibility
			mirrorOf[ch.TachPath] = ch.MirrorOf
		}
		if visByPath[fan1Path] != VisibilityReal {
			t.Errorf("fan1 should be real; got %q (full: %+v)", visByPath[fan1Path], got)
		}
		for _, p := range []string{fan2Path, fan3Path} {
			if visByPath[p] != VisibilityMirror {
				t.Errorf("%s should be mirror; got %q", p, visByPath[p])
			}
			if mirrorOf[p] != fan1Path {
				t.Errorf("%s mirror-of=%q want %s", p, mirrorOf[p], fan1Path)
			}
		}
		if visByPath[fan4Path] != VisibilityPhantom {
			t.Errorf("fan4 should be phantom; got %q", visByPath[fan4Path])
		}
	})
}

// TestEnumerateMonitorChannels_HandlesAbsentRoot returns nil on a
// missing hwmon root rather than panicking. Guards the
// container/restricted-sysfs fallback path.
func TestEnumerateMonitorChannels_HandlesAbsentRoot(t *testing.T) {
	sysFS := fstest.MapFS{}
	read := func(string) (int, error) { return 0, nil }
	got := EnumerateMonitorChannels(sysFS, "sys/class/hwmon", read, nil)
	if got != nil {
		t.Errorf("expected nil channels on absent root; got %d", len(got))
	}
}
