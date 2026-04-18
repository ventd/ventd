package pwmsys

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/testfixture/fakepwmsys"
)

func TestEnumerate_RPi5(t *testing.T) {
	fake := fakepwmsys.New(t, fakepwmsys.RPi5())
	b := newBackend(fake.Root, nil)

	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	// 2 chips × 2 channels = 4
	if len(chs) != 4 {
		t.Fatalf("Enumerate returned %d channels, want 4", len(chs))
	}
	for _, ch := range chs {
		if ch.ID == "" {
			t.Error("channel ID must not be empty")
		}
		st, ok := ch.Opaque.(State)
		if !ok {
			t.Errorf("channel %q: Opaque is %T, want State", ch.ID, ch.Opaque)
			continue
		}
		if st.PeriodNs == 0 {
			t.Errorf("channel %q: PeriodNs must not be 0", ch.ID)
		}
	}
}

func TestEnumerate_Idempotent(t *testing.T) {
	fake := fakepwmsys.New(t, fakepwmsys.RPi5())
	b := newBackend(fake.Root, nil)

	chs1, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("first Enumerate: %v", err)
	}
	chs2, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("second Enumerate: %v", err)
	}
	if len(chs1) != len(chs2) {
		t.Fatalf("Enumerate not idempotent: first=%d second=%d", len(chs1), len(chs2))
	}
	for i := range chs1 {
		if chs1[i].ID != chs2[i].ID {
			t.Errorf("channel %d: ID changed between calls: %q → %q", i, chs1[i].ID, chs2[i].ID)
		}
	}
}

func TestEnumerate_EmptyRoot(t *testing.T) {
	b := newBackend("/nonexistent/pwm/root", nil)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate on missing root: %v", err)
	}
	if len(chs) != 0 {
		t.Fatalf("expected 0 channels on missing root, got %d", len(chs))
	}
}

func TestWrite_DutyCycleTranslation(t *testing.T) {
	fake := fakepwmsys.New(t, fakepwmsys.RPi5())
	b := newBackend(fake.Root, nil)

	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(chs) == 0 {
		t.Fatal("no channels enumerated")
	}
	ch := chs[0]
	st := ch.Opaque.(State)
	period := st.PeriodNs

	cases := []uint8{0, 1, 128, 254, 255}
	for _, pwm := range cases {
		if err := b.Write(ch, pwm); err != nil {
			t.Fatalf("Write(%d): %v", pwm, err)
		}
		rel := filepath.Join(filepath.Base(filepath.Dir(st.ChanDir)), filepath.Base(st.ChanDir), "duty_cycle")
		duty, err := fake.ReadUint(rel)
		if err != nil {
			t.Fatalf("read duty_cycle after Write(%d): %v", pwm, err)
		}
		expectedDuty := uint64(float64(pwm) / 255.0 * float64(period))
		// Allow ±1 ns rounding tolerance (signed comparison avoids uint underflow at 0).
		diff := int64(duty) - int64(expectedDuty)
		if diff < -1 || diff > 1 {
			t.Errorf("Write(%d): duty_cycle=%d, want %d±1 (period=%d)", pwm, duty, expectedDuty, period)
		}
	}
}

func TestWrite_EnableSet(t *testing.T) {
	fake := fakepwmsys.New(t, fakepwmsys.RPi5())
	b := newBackend(fake.Root, nil)

	chs, _ := b.Enumerate(context.Background())
	ch := chs[0]
	st := ch.Opaque.(State)

	if err := b.Write(ch, 128); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rel := filepath.Join(filepath.Base(filepath.Dir(st.ChanDir)), filepath.Base(st.ChanDir), "enable")
	enable, err := fake.ReadUint(rel)
	if err != nil {
		t.Fatalf("read enable: %v", err)
	}
	if enable != 1 {
		t.Errorf("enable=%d, want 1 after Write", enable)
	}
}

func TestRead_RoundTrip(t *testing.T) {
	fake := fakepwmsys.New(t, fakepwmsys.RPi5())
	b := newBackend(fake.Root, nil)

	chs, _ := b.Enumerate(context.Background())
	ch := chs[0]

	wantPWM := uint8(128)
	if err := b.Write(ch, wantPWM); err != nil {
		t.Fatalf("Write: %v", err)
	}
	r, err := b.Read(ch)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !r.OK {
		t.Fatal("Read.OK=false")
	}
	// Round-trip within ±1 step.
	diff := int(r.PWM) - int(wantPWM)
	if diff < -1 || diff > 1 {
		t.Errorf("round-trip: wrote %d, read back %d (diff %d, want ±1)", wantPWM, r.PWM, diff)
	}
}

func TestRestore_DisablesChannel(t *testing.T) {
	fake := fakepwmsys.New(t, fakepwmsys.RPi5())
	b := newBackend(fake.Root, nil)

	chs, _ := b.Enumerate(context.Background())
	ch := chs[0]
	st := ch.Opaque.(State)

	// Write first so enable=1 is set.
	if err := b.Write(ch, 200); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := b.Restore(ch); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	rel := filepath.Join(filepath.Base(filepath.Dir(st.ChanDir)), filepath.Base(st.ChanDir), "enable")
	enable, err := fake.ReadUint(rel)
	if err != nil {
		t.Fatalf("read enable after Restore: %v", err)
	}
	if enable != 0 {
		t.Errorf("enable=%d after Restore, want 0", enable)
	}
}

func TestRestore_SafeOnUnopened(t *testing.T) {
	fake := fakepwmsys.New(t, fakepwmsys.RPi5())
	b := newBackend(fake.Root, nil)

	chs, _ := b.Enumerate(context.Background())
	ch := chs[0]

	// Restore without a prior Write must not panic or return an error.
	if err := b.Restore(ch); err != nil {
		t.Fatalf("Restore on never-opened channel: %v", err)
	}
}

func TestReEnumerate_ChannelReappears(t *testing.T) {
	fake := fakepwmsys.New(t, fakepwmsys.RPi5())
	b := newBackend(fake.Root, nil)

	chs1, _ := b.Enumerate(context.Background())
	if len(chs1) != 4 {
		t.Fatalf("first Enumerate: want 4, got %d", len(chs1))
	}

	// Restore all channels (simulates daemon shutdown).
	for _, ch := range chs1 {
		_ = b.Restore(ch)
	}

	// Re-enumerate: channels should reappear correctly.
	chs2, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("second Enumerate: %v", err)
	}
	if len(chs2) != 4 {
		t.Fatalf("second Enumerate: want 4, got %d", len(chs2))
	}
}

func TestClose_Idempotent(t *testing.T) {
	b := newBackend(t.TempDir(), nil)
	if err := b.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
