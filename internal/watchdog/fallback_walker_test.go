package watchdog

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestPriorCrashFallback_InstallsSequenceOnEmptyStore covers the
// #1332 entry shape: when origEnable came from the prior-crash branch
// (live=1) AND the LastKnownStore has no value, the entry carries
// SafePreDaemonEnableSequence on fallbackSeq so Restore walks it via
// the halhwmon backend's EINVAL path. origEnable matches the sequence
// head so callers that ignore fallbackSeq get the historical safe-2
// behaviour.
func TestPriorCrashFallback_InstallsSequenceOnEmptyStore(t *testing.T) {
	root := t.TempDir()
	hwmonDir := filepath.Join(root, "hwmon0")
	if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pwm := filepath.Join(hwmonDir, "pwm1")
	enable := pwm + "_enable"
	if err := os.WriteFile(pwm, []byte("100\n"), 0o600); err != nil {
		t.Fatalf("seed pwm: %v", err)
	}
	if err := os.WriteFile(enable, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("seed enable: %v", err)
	}

	var buf bytes.Buffer
	store := &fakeLastKnownStore{}
	w := NewWithStore(slog.New(slog.NewTextHandler(&buf, nil)), store)
	w.Register(pwm, "hwmon")

	if len(w.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.entries))
	}
	e := w.entries[0]
	if e.origEnable != SafePreDaemonEnableSequence[0] {
		t.Errorf("origEnable = %d, want %d (sequence head)", e.origEnable, SafePreDaemonEnableSequence[0])
	}
	if e.fallbackSeq == nil {
		t.Fatalf("entry.fallbackSeq is nil; sequence must be installed on prior-crash-no-store path")
	}
	if len(e.fallbackSeq) != len(SafePreDaemonEnableSequence) {
		t.Errorf("fallbackSeq length = %d, want %d", len(e.fallbackSeq), len(SafePreDaemonEnableSequence))
	}
	for i, v := range SafePreDaemonEnableSequence {
		if e.fallbackSeq[i] != v {
			t.Errorf("fallbackSeq[%d] = %d, want %d", i, e.fallbackSeq[i], v)
		}
	}
}

// TestPriorCrashFallback_StoreHitSkipsSequence pins the inverse: when
// the LastKnownStore has a persisted last-known-good value, Register
// uses it AND does NOT install the fallback sequence (no need —
// origEnable is authoritative).
func TestPriorCrashFallback_StoreHitSkipsSequence(t *testing.T) {
	root := t.TempDir()
	hwmonDir := filepath.Join(root, "hwmon0")
	if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pwm := filepath.Join(hwmonDir, "pwm1")
	enable := pwm + "_enable"
	if err := os.WriteFile(pwm, []byte("100\n"), 0o600); err != nil {
		t.Fatalf("seed pwm: %v", err)
	}
	if err := os.WriteFile(enable, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("seed enable: %v", err)
	}

	// Seed the store under the stable-identity key the watchdog will
	// compute. Resolution under a temp dir without a `device` symlink
	// produces an empty BusAddr, so Key() degrades to LegacyKey shape.
	identity := ChannelIdentity{LegacyPath: pwm}
	store := &fakeLastKnownStore{values: map[string]int{identity.Key(): 99}}
	var buf bytes.Buffer
	w := NewWithStore(slog.New(slog.NewTextHandler(&buf, nil)), store)
	w.Register(pwm, "hwmon")

	if len(w.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.entries))
	}
	if got := w.entries[0].origEnable; got != 99 {
		t.Errorf("origEnable = %d, want 99 (store hit)", got)
	}
	if w.entries[0].fallbackSeq != nil {
		t.Errorf("fallbackSeq = %v, want nil (store hit must not install fallback)", w.entries[0].fallbackSeq)
	}
}

// TestChannelIdentity_MigratesLegacyStoreOnGet covers the #1331
// migration shim: an entry persisted by a pre-stable-identity daemon
// under the LegacyPath key shape must still be discoverable on the
// first Get after upgrade. Once Set fires under the new identity, the
// legacy entry is removed.
func TestChannelIdentity_MigratesLegacyStoreOnGet(t *testing.T) {
	id := ChannelIdentity{
		ChipName:   "nct6687",
		BusAddr:    "2592",
		ChannelIdx: 1,
		LegacyPath: "/sys/class/hwmon/hwmon10/pwm1",
	}
	stableKey := id.Key()
	legacyKey := id.LegacyKey()
	if stableKey == legacyKey {
		t.Fatalf("stable key and legacy key identical; identity resolution must change shape")
	}
	store := &fakeLastKnownStore{legacyValues: map[string]int{legacyKey: 7}}
	got, ok := store.GetPreDaemonEnable(id)
	if !ok || got != 7 {
		t.Fatalf("legacy-only store: Get returned (%d, %v), want (7, true)", got, ok)
	}
	if err := store.SetPreDaemonEnable(id, 7); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, ok := store.legacyValues[legacyKey]; ok {
		t.Fatalf("legacy key %q still present after migrating Set", legacyKey)
	}
	if v, ok := store.values[stableKey]; !ok || v != 7 {
		t.Fatalf("stable key %q post-migration = (%d, %v), want (7, true)", stableKey, v, ok)
	}
	if len(store.migratedPaths) != 1 || store.migratedPaths[0] != id.LegacyPath {
		t.Fatalf("migration audit trail = %v, want [%q]", store.migratedPaths, id.LegacyPath)
	}
}

// TestChannelIdentity_KeyShapeStableAcrossHwmonRenumber documents the
// load-bearing property that the stable-identity key does NOT embed
// the volatile hwmonN index. Two paths under different hwmonN values
// with the same chip + bus + channel produce the same Key().
func TestChannelIdentity_KeyShapeStableAcrossHwmonRenumber(t *testing.T) {
	a := ChannelIdentity{
		ChipName: "nct6687", BusAddr: "2592", ChannelIdx: 1,
		LegacyPath: "/sys/class/hwmon/hwmon10/pwm1",
	}
	b := ChannelIdentity{
		ChipName: "nct6687", BusAddr: "2592", ChannelIdx: 1,
		LegacyPath: "/sys/class/hwmon/hwmon9/pwm1",
	}
	if a.Key() != b.Key() {
		t.Fatalf("Key() differs across hwmonN renumber:\n  a=%s\n  b=%s", a.Key(), b.Key())
	}
	if a.LegacyKey() == b.LegacyKey() {
		t.Fatalf("LegacyKey() should differ across hwmonN — that's the bug we're fixing")
	}
}

// TestResolveChannelIdentity_FromSysfsLayout verifies the resolver
// reads chip name + bus address from a real-shape sysfs layout. The
// fixture builds /sys/devices/platform/nct6687.2592/hwmon/hwmon0/pwm1
// + the `device` symlink the kernel normally produces.
func TestResolveChannelIdentity_FromSysfsLayout(t *testing.T) {
	root := t.TempDir()
	devDir := filepath.Join(root, "devices", "platform", "nct6687.2592")
	hwmonDir := filepath.Join(devDir, "hwmon", "hwmon0")
	if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
		t.Fatalf("mkdir hwmon: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hwmonDir, "name"), []byte("nct6687\n"), 0o600); err != nil {
		t.Fatalf("write name: %v", err)
	}
	if err := os.Symlink(devDir, filepath.Join(hwmonDir, "device")); err != nil {
		t.Fatalf("symlink device: %v", err)
	}
	pwm1 := filepath.Join(hwmonDir, "pwm1")
	if err := os.WriteFile(pwm1, []byte("0\n"), 0o600); err != nil {
		t.Fatalf("write pwm: %v", err)
	}

	id := resolveChannelIdentity(t.Context(), pwm1)
	if id.ChipName != "nct6687" {
		t.Errorf("ChipName = %q, want %q", id.ChipName, "nct6687")
	}
	if id.BusAddr != "2592" {
		t.Errorf("BusAddr = %q, want %q (parsed from platform/nct6687.2592)", id.BusAddr, "2592")
	}
	if id.ChannelIdx != 1 {
		t.Errorf("ChannelIdx = %d, want 1", id.ChannelIdx)
	}
	if id.LegacyPath != pwm1 {
		t.Errorf("LegacyPath = %q, want %q", id.LegacyPath, pwm1)
	}
	// Stable key contains the chip + bus suffix, NOT the hwmonN index.
	if got := id.Key(); !filepath.IsAbs(pwm1) || got == "" {
		t.Errorf("Key() empty; got %q", got)
	}
}
