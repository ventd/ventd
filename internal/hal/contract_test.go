// package hal_test pins the FanBackend contract invariants for every backend
// that ships today. Each subtest corresponds to one rule in
// .claude/rules/hal-contract.md; the names must match exactly so rulelint
// can verify the binding.
//
// Adding a new backend: append a backendCase to the cases slice in
// TestHAL_Contract. Do not add backend-scoped subtests without updating
// hal-contract.md with a matching Bound: line.
package hal_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	halHwmon "github.com/ventd/ventd/internal/hal/hwmon"
	halNVML "github.com/ventd/ventd/internal/hal/nvml"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/testfixture/fakehwmon"
)

// backendCase is the per-backend harness for contract tests.
type backendCase struct {
	name    string
	backend hal.FanBackend
	// mkCh builds a channel the test can use for Read / Write / Restore.
	// For hwmon it points at a real (fake) sysfs tree; for NVML it
	// constructs a channel opaque without requiring NVML to be present.
	mkCh func(t *testing.T) hal.Channel
	// fileBacked is true when mkCh produces channels whose state is
	// observable by reading sysfs files with os.ReadFile. hwmon: true;
	// nvml: false (state lives inside the driver, not a file).
	fileBacked bool
}

func hwmonBackendCase() backendCase {
	return backendCase{
		name:       "hwmon",
		backend:    halHwmon.NewBackend(nil),
		fileBacked: true,
		mkCh: func(t *testing.T) hal.Channel {
			t.Helper()
			fake := fakehwmon.New(t, &fakehwmon.Options{
				Chips: []fakehwmon.ChipOptions{{
					Name: "testchip",
					PWMs: []fakehwmon.PWMOptions{{
						Index:  1,
						PWM:    128,
						Enable: 2, // auto mode — Write will flip to 1
					}},
					Fans: []fakehwmon.FanOptions{{
						Index: 1,
						RPM:   1200,
					}},
				}},
			})
			pwmPath := filepath.Join(fake.Root, "hwmon0", "pwm1")
			return hal.Channel{
				ID:   pwmPath,
				Role: hal.RoleUnknown,
				Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
				Opaque: halHwmon.State{
					PWMPath:    pwmPath,
					OrigEnable: 2,
				},
			}
		},
	}
}

func nvmlBackendCase() backendCase {
	return backendCase{
		name:       "nvml",
		backend:    halNVML.NewBackend(nil),
		fileBacked: false,
		mkCh: func(t *testing.T) hal.Channel {
			t.Helper()
			return hal.Channel{
				ID:     "0",
				Role:   hal.RoleGPU,
				Caps:   hal.CapRead | hal.CapWritePWM | hal.CapRestore,
				Opaque: halNVML.State{Index: "0"},
			}
		},
	}
}

// channelIDs returns channel IDs from chs, sorted for deterministic comparison.
func channelIDs(chs []hal.Channel) []string {
	ids := make([]string, len(chs))
	for i, ch := range chs {
		ids[i] = ch.ID
	}
	sort.Strings(ids)
	return ids
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// readPWMInt reads the integer value from a hwmon channel's pwm sysfs file.
func readPWMInt(t *testing.T, ch hal.Channel) int {
	t.Helper()
	st := ch.Opaque.(halHwmon.State)
	data, err := os.ReadFile(st.PWMPath)
	if err != nil {
		t.Fatalf("readPWMInt %s: %v", st.PWMPath, err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("readPWMInt parse %q: %v", data, err)
	}
	return v
}

// TestHAL_Contract is the rule-to-test index for the FanBackend interface.
// Each subtest name binds one invariant from .claude/rules/hal-contract.md.
func TestHAL_Contract(t *testing.T) {
	cases := []backendCase{hwmonBackendCase(), nvmlBackendCase()}

	// ---------- RULE-HAL-001: Enumerate is idempotent ----------

	t.Run("enumerate_idempotent", func(t *testing.T) {
		for _, bc := range cases {
			bc := bc
			t.Run(bc.name, func(t *testing.T) {
				ctx := context.Background()
				chs1, err := bc.backend.Enumerate(ctx)
				if err != nil {
					t.Fatalf("Enumerate #1: %v", err)
				}
				chs2, err := bc.backend.Enumerate(ctx)
				if err != nil {
					t.Fatalf("Enumerate #2: %v", err)
				}
				ids1 := channelIDs(chs1)
				ids2 := channelIDs(chs2)
				if !equalStringSlices(ids1, ids2) {
					t.Errorf("Enumerate not idempotent: first=%v second=%v", ids1, ids2)
				}
			})
		}
	})

	// ---------- RULE-HAL-002: Read never mutates observable state ----------

	t.Run("read_no_mutation", func(t *testing.T) {
		for _, bc := range cases {
			bc := bc
			t.Run(bc.name, func(t *testing.T) {
				if !bc.fileBacked {
					if !nvidia.Available() {
						t.Skipf("backend %s: NVML not available; no file-backed state to observe mutation against", bc.name)
					}
				}
				ch := bc.mkCh(t)
				st := ch.Opaque.(halHwmon.State)
				before, err := os.ReadFile(st.PWMPath)
				if err != nil {
					t.Fatalf("read pwm before: %v", err)
				}
				_, _ = bc.backend.Read(ch)
				after, err := os.ReadFile(st.PWMPath)
				if err != nil {
					t.Fatalf("read pwm after: %v", err)
				}
				if string(before) != string(after) {
					t.Errorf("Read mutated pwm file: before=%q after=%q", before, after)
				}
			})
		}
	})

	// ---------- RULE-HAL-003: Write faithfully delivers the requested duty cycle ----------

	t.Run("write_faithful", func(t *testing.T) {
		for _, bc := range cases {
			bc := bc
			t.Run(bc.name, func(t *testing.T) {
				if !bc.fileBacked && !nvidia.Available() {
					t.Skipf("backend %s: NVML not available; cannot verify write fidelity without hardware", bc.name)
				}
				ch := bc.mkCh(t)
				const want uint8 = 100
				if err := bc.backend.Write(ch, want); err != nil {
					t.Fatalf("Write(%d): %v", want, err)
				}
				if !bc.fileBacked {
					// NVML: success return is the observable proof; no file to check.
					return
				}
				if got := readPWMInt(t, ch); got != int(want) {
					t.Errorf("Write(%d): pwm file contains %d", want, got)
				}
			})
		}
	})

	// ---------- RULE-HAL-004: Restore is safe on channels that were never opened ----------

	t.Run("restore_safe_on_unopened", func(t *testing.T) {
		for _, bc := range cases {
			bc := bc
			t.Run(bc.name, func(t *testing.T) {
				ch := bc.mkCh(t)
				if bc.fileBacked {
					// Set OrigEnable=-1 to simulate a channel that was enumerated
					// but never acquired by the watchdog (Write was never called).
					st := ch.Opaque.(halHwmon.State)
					st.OrigEnable = -1
					ch.Opaque = st
				}
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Restore panicked on un-opened channel: %v", r)
					}
				}()
				// A clean error is acceptable; a panic is not.
				_ = bc.backend.Restore(ch)
			})
		}
	})

	// ---------- RULE-HAL-005: Caps are stable across a channel's lifetime ----------

	t.Run("caps_stable", func(t *testing.T) {
		for _, bc := range cases {
			bc := bc
			t.Run(bc.name, func(t *testing.T) {
				ctx := context.Background()
				chs1, err := bc.backend.Enumerate(ctx)
				if err != nil {
					t.Fatalf("Enumerate #1: %v", err)
				}
				chs2, err := bc.backend.Enumerate(ctx)
				if err != nil {
					t.Fatalf("Enumerate #2: %v", err)
				}
				caps1 := make(map[string]hal.Caps, len(chs1))
				for _, ch := range chs1 {
					caps1[ch.ID] = ch.Caps
				}
				for _, ch := range chs2 {
					if c, ok := caps1[ch.ID]; ok && c != ch.Caps {
						t.Errorf("channel %q: Caps changed between enumerations: %v → %v", ch.ID, c, ch.Caps)
					}
				}
			})
		}
	})

	// ---------- RULE-HAL-006: ChannelRole classification is deterministic ----------

	t.Run("role_deterministic", func(t *testing.T) {
		for _, bc := range cases {
			bc := bc
			t.Run(bc.name, func(t *testing.T) {
				ctx := context.Background()
				chs1, err := bc.backend.Enumerate(ctx)
				if err != nil {
					t.Fatalf("Enumerate #1: %v", err)
				}
				chs2, err := bc.backend.Enumerate(ctx)
				if err != nil {
					t.Fatalf("Enumerate #2: %v", err)
				}
				roles1 := make(map[string]hal.ChannelRole, len(chs1))
				for _, ch := range chs1 {
					roles1[ch.ID] = ch.Role
				}
				for _, ch := range chs2 {
					if r, ok := roles1[ch.ID]; ok && r != ch.Role {
						t.Errorf("channel %q: Role changed between enumerations: %q → %q", ch.ID, r, ch.Role)
					}
				}
			})
		}
	})

	// ---------- RULE-HAL-007: Close is idempotent ----------

	t.Run("close_idempotent", func(t *testing.T) {
		for _, bc := range cases {
			bc := bc
			t.Run(bc.name, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Close panicked: %v", r)
					}
				}()
				if err := bc.backend.Close(); err != nil {
					t.Errorf("Close #1: %v", err)
				}
				if err := bc.backend.Close(); err != nil {
					t.Errorf("Close #2: %v", err)
				}
			})
		}
	})

	// ---------- RULE-HAL-008: Writing to an already-acquired channel is a no-op or clean error ----------

	t.Run("write_idempotent_open", func(t *testing.T) {
		for _, bc := range cases {
			bc := bc
			t.Run(bc.name, func(t *testing.T) {
				ch := bc.mkCh(t)
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("second Write panicked: %v", r)
					}
				}()
				// Two successive writes must not panic; errors are acceptable.
				_ = bc.backend.Write(ch, 100)
				_ = bc.backend.Write(ch, 200)
				if !bc.fileBacked {
					return // cannot observe file state
				}
				// Last write must win.
				if got := readPWMInt(t, ch); got != 200 {
					t.Errorf("after writes [100, 200]: pwm=%d, want 200", got)
				}
				// pwm_enable must be in manual mode (1), not re-acquired.
				st := ch.Opaque.(halHwmon.State)
				enableData, err := os.ReadFile(st.PWMPath + "_enable")
				if err != nil {
					t.Fatalf("read pwm_enable: %v", err)
				}
				if v, _ := strconv.Atoi(strings.TrimSpace(string(enableData))); v != 1 {
					t.Errorf("pwm_enable=%d after double write, want 1 (manual mode not corrupted)", v)
				}
			})
		}
	})
}
