// SPDX-License-Identifier: GPL-3.0-or-later
package asuswmi

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// newFakeHwmon builds a fake /sys/class/hwmon tree containing one
// asus_custom_fan_curve device with the CPU (pwm1) and GPU (pwm2) fan blocks,
// and returns a Backend rooted at it plus the hwmon directory. Each fan's
// pwmN_auto_point1_pwm probe file exists so Enumerate detects it.
func newFakeHwmon(t *testing.T, fans ...int) (*Backend, string) {
	t.Helper()
	if len(fans) == 0 {
		fans = []int{cpuFanIndex, gpuFanIndex}
	}
	root := t.TempDir()
	hwmonDir := filepath.Join(root, "hwmon3")
	if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
		t.Fatalf("mkdir hwmon: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hwmonDir, "name"), []byte(HwmonName+"\n"), 0o644); err != nil {
		t.Fatalf("write name: %v", err)
	}
	for _, f := range fans {
		probe := filepath.Join(hwmonDir, "pwm"+strconv.Itoa(f)+"_auto_point1_pwm")
		if err := os.WriteFile(probe, []byte("0\n"), 0o644); err != nil {
			t.Fatalf("write probe: %v", err)
		}
	}
	b := NewBackend(nil)
	b.hwmonRoot = root
	return b, hwmonDir
}

func readInt(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse %s = %q: %v", path, data, err)
	}
	return v
}

// ---------- RULE-HAL-ASUS-01: Enumerate is idempotent and keys on the asus_custom_fan_curve hwmon name ----------

func TestEnumerate(t *testing.T) {
	t.Run("finds_cpu_and_gpu_fans", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		chs, err := b.Enumerate(context.Background())
		if err != nil {
			t.Fatalf("Enumerate: %v", err)
		}
		if len(chs) != 2 {
			t.Fatalf("got %d channels, want 2 (CPU + GPU)", len(chs))
		}
		wantRole := map[string]hal.ChannelRole{
			filepath.Join(hwmonDir, "pwm1"): hal.RoleCPU,
			filepath.Join(hwmonDir, "pwm2"): hal.RoleGPU,
		}
		for _, ch := range chs {
			if ch.Caps&hal.CapWriteCurve == 0 {
				t.Errorf("channel %q missing CapWriteCurve", ch.ID)
			}
			if ch.Caps&hal.CapWritePWM != 0 {
				t.Errorf("channel %q must NOT advertise CapWritePWM (curve-only surface)", ch.ID)
			}
			if ch.Caps&(hal.CapRead|hal.CapRestore) != (hal.CapRead | hal.CapRestore) {
				t.Errorf("channel %q missing CapRead|CapRestore: %v", ch.ID, ch.Caps)
			}
			if got, ok := wantRole[ch.ID]; !ok {
				t.Errorf("unexpected channel ID %q", ch.ID)
			} else if ch.Role != got {
				t.Errorf("channel %q role = %q, want %q", ch.ID, ch.Role, got)
			}
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		b, _ := newFakeHwmon(t)
		first, err := b.Enumerate(context.Background())
		if err != nil {
			t.Fatalf("Enumerate #1: %v", err)
		}
		second, err := b.Enumerate(context.Background())
		if err != nil {
			t.Fatalf("Enumerate #2: %v", err)
		}
		if len(first) != len(second) {
			t.Fatalf("non-idempotent: %d vs %d channels", len(first), len(second))
		}
		for i := range first {
			if first[i].ID != second[i].ID || first[i].Caps != second[i].Caps || first[i].Role != second[i].Role {
				t.Errorf("channel %d differs between enumerations: %+v vs %+v", i, first[i], second[i])
			}
		}
	})

	t.Run("cpu_only_when_no_gpu_fan", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t, cpuFanIndex)
		chs, err := b.Enumerate(context.Background())
		if err != nil {
			t.Fatalf("Enumerate: %v", err)
		}
		if len(chs) != 1 {
			t.Fatalf("got %d channels, want 1 (CPU only)", len(chs))
		}
		if chs[0].ID != filepath.Join(hwmonDir, "pwm1") || chs[0].Role != hal.RoleCPU {
			t.Errorf("CPU-only channel = %+v", chs[0])
		}
	})

	t.Run("non_asus_host_empty", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		// Rewrite the name to a non-ASUS chip; Enumerate must stay quiet.
		if err := os.WriteFile(filepath.Join(hwmonDir, "name"), []byte("nct6687\n"), 0o644); err != nil {
			t.Fatalf("rewrite name: %v", err)
		}
		chs, err := b.Enumerate(context.Background())
		if err != nil {
			t.Fatalf("Enumerate: %v", err)
		}
		if len(chs) != 0 {
			t.Fatalf("got %d channels on non-ASUS host, want 0", len(chs))
		}
	})

	t.Run("missing_hwmon_root_empty", func(t *testing.T) {
		b := NewBackend(nil)
		b.hwmonRoot = filepath.Join(t.TempDir(), "does-not-exist")
		chs, err := b.Enumerate(context.Background())
		if err != nil {
			t.Fatalf("Enumerate on absent root must not error: %v", err)
		}
		if len(chs) != 0 {
			t.Fatalf("got %d channels, want 0", len(chs))
		}
	})
}

// ---------- RULE-HAL-ASUS-02: WriteCurve programs eight anchors and applies the curve via pwm_enable=1 ----------

func TestWriteCurve(t *testing.T) {
	t.Run("writes_eight_anchors_and_enables", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		ch := hal.Channel{
			ID:     filepath.Join(hwmonDir, "pwm1"),
			Role:   hal.RoleCPU,
			Caps:   hal.CapRead | hal.CapWriteCurve | hal.CapRestore,
			Opaque: State{HwmonDir: hwmonDir, FanIndex: cpuFanIndex},
		}
		// A 0%→100% ramp from 40°C to 90°C.
		points := []hal.CurvePoint{{TempC: 40, Pct: 0}, {TempC: 90, Pct: 100}}
		if err := b.WriteCurve(ch, points); err != nil {
			t.Fatalf("WriteCurve: %v", err)
		}
		// All eight temp + pwm points must exist, with strictly-increasing
		// temps and non-decreasing pwm bytes in 0-255.
		prevTemp, prevPWM := -1, -1
		for n := 1; n <= fanCurvePoints; n++ {
			temp := readInt(t, filepath.Join(hwmonDir, "pwm1_auto_point"+strconv.Itoa(n)+"_temp"))
			pwm := readInt(t, filepath.Join(hwmonDir, "pwm1_auto_point"+strconv.Itoa(n)+"_pwm"))
			if temp <= prevTemp {
				t.Errorf("point %d temp %d not strictly increasing (prev %d)", n, temp, prevTemp)
			}
			if pwm < prevPWM {
				t.Errorf("point %d pwm %d decreased (prev %d)", n, pwm, prevPWM)
			}
			if pwm < 0 || pwm > 255 {
				t.Errorf("point %d pwm %d out of 0-255", n, pwm)
			}
			prevTemp, prevPWM = temp, pwm
		}
		// The first anchor is 0% and the last is 100% → 0 and 255 PWM bytes.
		if got := readInt(t, filepath.Join(hwmonDir, "pwm1_auto_point1_pwm")); got != 0 {
			t.Errorf("first pwm byte = %d, want 0", got)
		}
		if got := readInt(t, filepath.Join(hwmonDir, "pwm1_auto_point8_pwm")); got != 255 {
			t.Errorf("last pwm byte = %d, want 255", got)
		}
		// The curve is applied (manual mode).
		if got := readInt(t, filepath.Join(hwmonDir, "pwm1_enable")); got != enableManual {
			t.Errorf("pwm1_enable = %d, want %d (manual)", got, enableManual)
		}
	})

	t.Run("firmware_refusal_wrapped", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		// Inject an EIO on the enable write — the "BIOS rejected fan curve" path.
		b.writeFile = func(path string, data []byte, perm os.FileMode) error {
			if strings.HasSuffix(path, "pwm1_enable") {
				return &fs.PathError{Op: "write", Path: path, Err: errors.New("input/output error")}
			}
			return os.WriteFile(path, data, perm)
		}
		ch := hal.Channel{Opaque: State{HwmonDir: hwmonDir, FanIndex: cpuFanIndex}}
		err := b.WriteCurve(ch, []hal.CurvePoint{{TempC: 40, Pct: 30}, {TempC: 90, Pct: 90}})
		if !errors.Is(err, ErrFanCurveRefused) {
			t.Fatalf("WriteCurve error = %v, want ErrFanCurveRefused", err)
		}
	})

	t.Run("permission_denied_wrapped", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		b.writeFile = func(path string, data []byte, perm os.FileMode) error {
			return &fs.PathError{Op: "write", Path: path, Err: fs.ErrPermission}
		}
		ch := hal.Channel{Opaque: State{HwmonDir: hwmonDir, FanIndex: cpuFanIndex}}
		err := b.WriteCurve(ch, []hal.CurvePoint{{TempC: 40, Pct: 30}})
		if !errors.Is(err, hal.ErrNotPermitted) {
			t.Fatalf("WriteCurve error = %v, want hal.ErrNotPermitted", err)
		}
	})
}

// ---------- RULE-HAL-ASUS-03: Restore hands the fan back to factory auto (pwm_enable=2), safe on un-programmed channels ----------

func TestRestore(t *testing.T) {
	t.Run("writes_enable_auto", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		ch := hal.Channel{Opaque: State{HwmonDir: hwmonDir, FanIndex: gpuFanIndex}}
		if err := b.Restore(ch); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if got := readInt(t, filepath.Join(hwmonDir, "pwm2_enable")); got != enableAuto {
			t.Errorf("pwm2_enable = %d, want %d (factory auto)", got, enableAuto)
		}
	})

	t.Run("safe_on_unprogrammed_channel", func(t *testing.T) {
		// A channel that was enumerated but never had WriteCurve called: Restore
		// must be a clean write, never a panic (RULE-HAL-004).
		b, hwmonDir := newFakeHwmon(t)
		ch := hal.Channel{Opaque: State{HwmonDir: hwmonDir, FanIndex: cpuFanIndex}}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Restore panicked on un-programmed channel: %v", r)
			}
		}()
		if err := b.Restore(ch); err != nil {
			t.Fatalf("Restore on un-programmed channel: %v", err)
		}
	})
}

// ---------- RULE-HAL-ASUS-04: Read never mutates and reports a duty only when the kernel exposes one ----------

func TestRead(t *testing.T) {
	t.Run("no_mutation", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		pwmPath := filepath.Join(hwmonDir, "pwm1")
		if err := os.WriteFile(pwmPath, []byte("128\n"), 0o644); err != nil {
			t.Fatalf("seed pwm1: %v", err)
		}
		before, _ := os.ReadFile(pwmPath)
		ch := hal.Channel{Opaque: State{HwmonDir: hwmonDir, FanIndex: cpuFanIndex}}
		if _, err := b.Read(ch); err != nil {
			t.Fatalf("Read: %v", err)
		}
		after, _ := os.ReadFile(pwmPath)
		if string(before) != string(after) {
			t.Errorf("Read mutated pwm1: before=%q after=%q", before, after)
		}
	})

	t.Run("reports_duty_when_present", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		if err := os.WriteFile(filepath.Join(hwmonDir, "pwm1"), []byte("200\n"), 0o644); err != nil {
			t.Fatalf("seed pwm1: %v", err)
		}
		ch := hal.Channel{Opaque: State{HwmonDir: hwmonDir, FanIndex: cpuFanIndex}}
		r, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if !r.OK || r.PWM != 200 {
			t.Errorf("Read = %+v, want OK with PWM 200", r)
		}
	})

	t.Run("not_ok_when_no_duty_node", func(t *testing.T) {
		// No bare pwm1 file: the curve hwmon doesn't surface instantaneous duty
		// on this model. Read returns OK=false (skip), never an error.
		b, hwmonDir := newFakeHwmon(t)
		ch := hal.Channel{Opaque: State{HwmonDir: hwmonDir, FanIndex: cpuFanIndex}}
		r, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if r.OK {
			t.Errorf("Read = %+v, want OK=false when no duty node", r)
		}
	})
}

// ---------- RULE-HAL-ASUS-05: Write (per-tick duty) is refused — the surface is curve-only ----------

func TestWritePerTickRefused(t *testing.T) {
	t.Run("returns_error", func(t *testing.T) {
		b, hwmonDir := newFakeHwmon(t)
		ch := hal.Channel{
			ID:     filepath.Join(hwmonDir, "pwm1"),
			Opaque: State{HwmonDir: hwmonDir, FanIndex: cpuFanIndex},
		}
		if err := b.Write(ch, 128); err == nil {
			t.Fatal("Write returned nil, want a curve-only error")
		}
		// And it must not have created an enable file (no side effect).
		if _, err := os.Stat(filepath.Join(hwmonDir, "pwm1_enable")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Write created pwm1_enable; want no side effect")
		}
	})
}

// ---------- RULE-HAL-ASUS-06: invalid channel state is rejected; Close is idempotent ----------

func TestStateValidation(t *testing.T) {
	t.Run("empty_hwmondir_rejected", func(t *testing.T) {
		b := NewBackend(nil)
		_, err := b.Read(hal.Channel{Opaque: State{FanIndex: 1}})
		if !errors.Is(err, ErrNoFanCurveHwmon) {
			t.Fatalf("Read with empty HwmonDir = %v, want ErrNoFanCurveHwmon", err)
		}
	})

	t.Run("bad_fan_index_rejected", func(t *testing.T) {
		b := NewBackend(nil)
		_, err := b.Read(hal.Channel{Opaque: State{HwmonDir: "/x", FanIndex: 7}})
		if err == nil {
			t.Fatal("Read with FanIndex 7 returned nil, want invalid-index error")
		}
	})

	t.Run("close_idempotent", func(t *testing.T) {
		b := NewBackend(nil)
		if err := b.Close(); err != nil {
			t.Fatalf("Close #1: %v", err)
		}
		if err := b.Close(); err != nil {
			t.Fatalf("Close #2: %v", err)
		}
	})
}

// ---------- RULE-HAL-ASUS-07: resampleCurve yields exactly eight monotonic anchors with percent→byte conversion ----------

func TestResampleCurve(t *testing.T) {
	t.Run("eight_monotonic_anchors", func(t *testing.T) {
		anchors, err := resampleCurve([]hal.CurvePoint{
			{TempC: 30, Pct: 10}, {TempC: 60, Pct: 50}, {TempC: 90, Pct: 100},
		})
		if err != nil {
			t.Fatalf("resampleCurve: %v", err)
		}
		if len(anchors) != fanCurvePoints {
			t.Fatalf("got %d anchors, want %d", len(anchors), fanCurvePoints)
		}
		for i := 1; i < len(anchors); i++ {
			if anchors[i].TempC <= anchors[i-1].TempC {
				t.Errorf("anchor %d temp %d not strictly increasing", i, anchors[i].TempC)
			}
			if anchors[i].PWM < anchors[i-1].PWM {
				t.Errorf("anchor %d pwm %d decreased", i, anchors[i].PWM)
			}
		}
	})

	t.Run("percent_to_byte_rounding", func(t *testing.T) {
		// 0→0, 100→255, 50→128 (round-half-up).
		cases := map[int]uint8{0: 0, 100: 255, 50: 128, 25: 64}
		for pct, want := range cases {
			if got := pctToPWMByte(pct); got != want {
				t.Errorf("pctToPWMByte(%d) = %d, want %d", pct, got, want)
			}
		}
	})

	t.Run("degenerate_flat_input_spreads_temps", func(t *testing.T) {
		// A single point (or flat span) must still yield eight strictly
		// increasing temperatures the firmware will accept.
		anchors, err := resampleCurve([]hal.CurvePoint{{TempC: 50, Pct: 40}})
		if err != nil {
			t.Fatalf("resampleCurve: %v", err)
		}
		if len(anchors) != fanCurvePoints {
			t.Fatalf("got %d anchors, want %d", len(anchors), fanCurvePoints)
		}
		for i := 1; i < len(anchors); i++ {
			if anchors[i].TempC <= anchors[i-1].TempC {
				t.Fatalf("flat input produced non-increasing temps: %+v", anchors)
			}
		}
	})

	t.Run("empty_input_errors", func(t *testing.T) {
		if _, err := resampleCurve(nil); err == nil {
			t.Fatal("resampleCurve(nil) returned nil error, want failure")
		}
	})
}
