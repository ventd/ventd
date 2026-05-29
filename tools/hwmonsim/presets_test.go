package main

import (
	"testing"

	hwhal "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwmon"
)

func TestBuildDevices_PresetDesktop(t *testing.T) {
	cfg := baseCfg()
	cfg.out = t.TempDir()
	cfg.preset = "desktop"
	devs, err := buildDevices(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		chip string
		fans int
	}{{"nct6687", 6}, {"amdgpu", 1}, {"nvme", 0}, {"acpitz", 0}, {"nvidia", 1}}
	if len(devs) != len(want) {
		t.Fatalf("desktop preset: %d devices, want %d", len(devs), len(want))
	}
	for i, w := range want {
		if devs[i].chip != w.chip || devs[i].fans != w.fans {
			t.Errorf("device %d = %s/%d fans, want %s/%d", i, devs[i].chip, devs[i].fans, w.chip, w.fans)
		}
	}
}

func TestBuildDevices_UnknownPreset(t *testing.T) {
	cfg := baseCfg()
	cfg.out = t.TempDir()
	cfg.preset = "nope"
	if _, err := buildDevices(cfg); err == nil {
		t.Fatal("expected error for unknown preset")
	}
}

// The desktop preset must enumerate, through the real HAL backend, exactly the
// controllable channels (nct6687's 6 + amdgpu's 1 = 7) and nothing else: the
// temp-only nvme/acpitz devices and the nvidia device are not control channels.
func TestPresetDesktop_HALEnumeratesOnlyControllable(t *testing.T) {
	cfg := baseCfg()
	cfg.out = t.TempDir()
	cfg.preset = "desktop"
	devs, err := buildDevices(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devs {
		if err := materialise(d.dir, d.chip, d.fans, d.temps); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv(hwmon.RootOverrideEnv, cfg.out)
	chans, err := hwhal.NewBackend(nil).Enumerate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 7 {
		t.Fatalf("HAL enumerated %d channels, want 7 (6 nct6687 + 1 amdgpu)", len(chans))
	}

	// Classification: nvme/acpitz are ClassNoFans, nvidia is skipped.
	classByChip := map[string]hwmon.CapabilityClass{}
	for _, dev := range hwmon.EnumerateDevices(cfg.out) {
		classByChip[dev.ChipName] = dev.Class
	}
	if classByChip["nvme"] != hwmon.ClassNoFans {
		t.Errorf("nvme class = %q, want ClassNoFans", classByChip["nvme"])
	}
	if classByChip["acpitz"] != hwmon.ClassNoFans {
		t.Errorf("acpitz class = %q, want ClassNoFans", classByChip["acpitz"])
	}
	if classByChip["nvidia"] != hwmon.ClassSkipNVIDIA {
		t.Errorf("nvidia class = %q, want ClassSkipNVIDIA", classByChip["nvidia"])
	}
}

func TestBoardFanCount(t *testing.T) {
	// No fan_profiles / pwm_groups → default.
	if n := boardFanCount(&hwdb.BoardCatalogEntry{}, 3); n != 3 {
		t.Errorf("empty board fan count = %d, want default 3", n)
	}
	// fan_profiles populated → its length (the real channel count).
	withProfiles := &hwdb.BoardCatalogEntry{
		FanProfiles: []hwdb.FanProfile{{Channel: "pwm1"}, {Channel: "pwm2"}, {Channel: "pwm3"}},
	}
	if n := boardFanCount(withProfiles, 99); n != 3 {
		t.Errorf("fan_profiles board fan count = %d, want 3", n)
	}
	// pwm_groups (no fan_profiles) → its length.
	withGroups := &hwdb.BoardCatalogEntry{
		PWMGroups: []hwdb.PWMGroup{{Channel: "pwm1"}, {Channel: "pwm2"}},
	}
	if n := boardFanCount(withGroups, 99); n != 2 {
		t.Errorf("pwm_groups board fan count = %d, want 2", n)
	}
}

// devicesFromBoard: primary chip gets the fan_profiles count, additional
// controllers follow, and unknown/nvidia chips are skipped.
func TestDevicesFromBoard_SeedsProfilesAndSkips(t *testing.T) {
	entry := &hwdb.BoardCatalogEntry{
		PrimaryController: hwdb.BoardController{Chip: "nct6687"},
		AdditionalControllers: []hwdb.BoardController{
			{Chip: "unknown"}, {Chip: "it87"}, {Chip: "nvidia"},
		},
		FanProfiles: []hwdb.FanProfile{{Channel: "pwm1"}, {Channel: "pwm2"}},
	}
	cfg := baseCfg()
	cfg.out = t.TempDir()
	cfg.fans = 5
	devs := devicesFromBoard(entry, cfg)
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want 2 (nct6687 + it87; unknown/nvidia skipped): %+v", len(devs), devs)
	}
	if devs[0].chip != "nct6687" || devs[0].fans != 2 {
		t.Errorf("primary = %s/%d, want nct6687/2 (from fan_profiles)", devs[0].chip, devs[0].fans)
	}
	if devs[1].chip != "it87" || devs[1].fans != 5 {
		t.Errorf("additional = %s/%d, want it87/5 (from --fans)", devs[1].chip, devs[1].fans)
	}
}
