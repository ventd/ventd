// SPDX-License-Identifier: GPL-3.0-or-later
package msiec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSensorPath(t *testing.T) {
	cases := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"cpu temp ok", "cpu/realtime_temperature", false},
		{"gpu temp ok", "gpu/realtime_temperature", false},
		{"cpu fan_speed ok", "cpu/realtime_fan_speed", false},
		{"gpu fan_speed ok", "gpu/realtime_fan_speed", false},
		{"cpu basic_fan_speed ok", "cpu/basic_fan_speed", false},
		{"gpu basic_fan_speed not exposed", "gpu/basic_fan_speed", true},
		{"empty path rejected", "", true},
		{"leds rejected", "leds/state", true},
		{"absolute path rejected", "/sys/devices/platform/msi-ec/cpu/realtime_temperature", true},
		{"traversal rejected", "../../../etc/passwd", true},
		{"unrelated subdir rejected", "cpu/uevent", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSensorPath(tc.rel)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("ValidateSensorPath(%q) err = %v, want err? %v", tc.rel, err, tc.wantErr)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrSensorPathNotAllowed) {
				t.Fatalf("ValidateSensorPath(%q) err = %v; want errors.Is(ErrSensorPathNotAllowed)", tc.rel, err)
			}
		})
	}
}

func TestReadSensor_Temperature(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cpu"), 0o755); err != nil {
		t.Fatalf("mkdir cpu: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cpu", "realtime_temperature"), []byte("63\n"), 0o644); err != nil {
		t.Fatalf("seed temp: %v", err)
	}
	got, err := ReadSensor(root, "cpu/realtime_temperature")
	if err != nil {
		t.Fatalf("ReadSensor: %v", err)
	}
	if got != 63 {
		t.Fatalf("ReadSensor cpu temp = %v, want 63 (raw °C, no millidegree scaling)", got)
	}
}

func TestReadSensor_FanSpeedPercent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gpu"), 0o755); err != nil {
		t.Fatalf("mkdir gpu: %v", err)
	}
	// GPU fan_speed can exceed 100 under cooler_boost — accept up to ~150.
	if err := os.WriteFile(filepath.Join(root, "gpu", "realtime_fan_speed"), []byte("117\n"), 0o644); err != nil {
		t.Fatalf("seed fan_speed: %v", err)
	}
	got, err := ReadSensor(root, "gpu/realtime_fan_speed")
	if err != nil {
		t.Fatalf("ReadSensor: %v", err)
	}
	if got != 117 {
		t.Fatalf("ReadSensor gpu fan_speed = %v, want 117 (raw percent)", got)
	}
}

func TestReadSensor_RejectsDisallowedPathBeforeIO(t *testing.T) {
	// Seed an arbitrary file outside the allowlist to prove that the
	// validator gate fires before any I/O — even if the path resolves
	// to a real file under the seeded root, ReadSensor must refuse it.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "leds"), 0o755); err != nil {
		t.Fatalf("mkdir leds: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "leds", "state"), []byte("1\n"), 0o644); err != nil {
		t.Fatalf("seed leds/state: %v", err)
	}
	if _, err := ReadSensor(root, "leds/state"); !errors.Is(err, ErrSensorPathNotAllowed) {
		t.Fatalf("ReadSensor leds/state err = %v; want ErrSensorPathNotAllowed", err)
	}
}

func TestReadSensor_MissingFile(t *testing.T) {
	root := t.TempDir()
	// Allowed path, but the seeded fixture omits the file entirely. The
	// controller treats this as "sensor unavailable this tick" and
	// skips — but ReadSensor still surfaces the raw I/O error so the
	// caller can log the underlying cause.
	if _, err := ReadSensor(root, "cpu/realtime_temperature"); err == nil {
		t.Fatalf("ReadSensor missing file: got nil err; want a read error")
	}
}

func TestReadSensor_ParseError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cpu"), 0o755); err != nil {
		t.Fatalf("mkdir cpu: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cpu", "realtime_temperature"), []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatalf("seed bad temp: %v", err)
	}
	if _, err := ReadSensor(root, "cpu/realtime_temperature"); err == nil {
		t.Fatalf("ReadSensor parse: got nil err; want a parse error")
	}
}
