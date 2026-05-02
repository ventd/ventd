package doctor

import (
	"log/slog"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/state"
)

// freshStore opens a temp KV store and wraps it in a SuppressionStore
// with an injectable clock starting at base. Tests advance the clock
// by mutating the returned *time.Time.
func freshStore(t *testing.T, base time.Time) (*SuppressionStore, *time.Time) {
	t.Helper()
	st, err := state.Open(t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := base
	store := NewSuppressionStore(st.KV, func() time.Time { return now })
	return store, &now
}

func TestRULE_DOCTOR_SUPPRESSION_RoundTrip(t *testing.T) {
	base := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	s, clock := freshStore(t, base)

	if s.IsSuppressed("kernel_update", "abc123") {
		t.Fatalf("fresh store reports suppressed; want false")
	}

	if err := s.Suppress("kernel_update", "abc123", "operator dismissed", time.Hour); err != nil {
		t.Fatalf("Suppress: %v", err)
	}

	if !s.IsSuppressed("kernel_update", "abc123") {
		t.Fatalf("after Suppress: IsSuppressed=false; want true")
	}

	// Advance clock past expiry — suppression auto-expires.
	*clock = base.Add(2 * time.Hour)
	if s.IsSuppressed("kernel_update", "abc123") {
		t.Fatalf("after expiry: IsSuppressed=true; want false")
	}
}

func TestRULE_DOCTOR_SUPPRESSION_AcknowledgeForever(t *testing.T) {
	base := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	s, clock := freshStore(t, base)

	// 100 years is the operational "forever" — capped by yaml/int64 round-trip.
	if err := s.Suppress("rpm_stuck", "fan2", "known false positive", 100*365*24*time.Hour); err != nil {
		t.Fatalf("Suppress: %v", err)
	}

	*clock = base.Add(50 * 365 * 24 * time.Hour) // 50 years later
	if !s.IsSuppressed("rpm_stuck", "fan2") {
		t.Fatalf("forever-suppressed entry expired after 50 years")
	}
}

func TestRULE_DOCTOR_SUPPRESSION_Unsuppress(t *testing.T) {
	base := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	s, _ := freshStore(t, base)

	if err := s.Suppress("apparmor_denial", "/sys/class/hwmon", "", time.Hour); err != nil {
		t.Fatalf("Suppress: %v", err)
	}
	if !s.IsSuppressed("apparmor_denial", "/sys/class/hwmon") {
		t.Fatalf("Suppress did not record")
	}

	if err := s.Unsuppress("apparmor_denial", "/sys/class/hwmon"); err != nil {
		t.Fatalf("Unsuppress: %v", err)
	}
	if s.IsSuppressed("apparmor_denial", "/sys/class/hwmon") {
		t.Fatalf("after Unsuppress: IsSuppressed=true; want false")
	}

	// Unsuppress on missing key is a no-op.
	if err := s.Unsuppress("apparmor_denial", "/never/existed"); err != nil {
		t.Errorf("Unsuppress on missing key returned error: %v", err)
	}
}

func TestSuppressionStore_NilSafe(t *testing.T) {
	var nilStore *SuppressionStore
	if nilStore.IsSuppressed("foo", "bar") {
		t.Fatalf("nil store reports suppressed; want false")
	}
}

func TestSuppressionStore_List(t *testing.T) {
	base := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	s, _ := freshStore(t, base)

	if err := s.Suppress("kernel_update", "abc", "", time.Hour); err != nil {
		t.Fatalf("Suppress: %v", err)
	}
	if err := s.Suppress("rpm_stuck", "fan2", "", time.Hour); err != nil {
		t.Fatalf("Suppress: %v", err)
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List len = %d, want 2; got map = %v", len(got), got)
	}
}
