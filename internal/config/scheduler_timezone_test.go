package config

import (
	"errors"
	"testing"
	"time"
)

// TestScheduler_Location_ResolvesUTC pins the canonical name path.
func TestScheduler_Location_ResolvesUTC(t *testing.T) {
	sc := Scheduler{Timezone: "utc"}
	loc, ok := sc.Location()
	if !ok {
		t.Fatalf("Location() ok=false for utc; want true")
	}
	if loc != time.UTC {
		t.Errorf("Location() = %v, want time.UTC", loc)
	}
}

func TestScheduler_Location_EmptyDefaultsToLocal(t *testing.T) {
	sc := Scheduler{}
	loc, ok := sc.Location()
	if !ok {
		t.Fatalf("Location() ok=false for empty; want true (default Local)")
	}
	if loc != time.Local {
		t.Errorf("Location() = %v, want time.Local", loc)
	}
}

func TestScheduler_Location_ExplicitLocalIsAlias(t *testing.T) {
	sc := Scheduler{Timezone: "Local"}
	loc, ok := sc.Location()
	if !ok || loc != time.Local {
		t.Errorf("Location()=(%v, %v); want (Local, true)", loc, ok)
	}
}

// TestScheduler_Location_IANANameResolves uses the test seam to assert
// the resolver consults time.LoadLocation for non-magic names. The
// real LoadLocation depends on tzdata availability at runtime which
// is brittle across distros; the seam keeps the test hermetic.
func TestScheduler_Location_IANANameResolves(t *testing.T) {
	want, _ := time.LoadLocation("UTC") // pseudo-stand-in
	prev := loadLocationFn
	t.Cleanup(func() { loadLocationFn = prev })
	loadLocationFn = func(name string) (*time.Location, error) {
		if name != "Australia/Sydney" {
			t.Errorf("LoadLocation seam called with %q; want %q", name, "Australia/Sydney")
		}
		return want, nil
	}

	sc := Scheduler{Timezone: "Australia/Sydney"}
	loc, ok := sc.Location()
	if !ok {
		t.Fatalf("Location() ok=false; want true (IANA name resolved)")
	}
	if loc != want {
		t.Errorf("Location() = %v, want %v", loc, want)
	}
}

// TestScheduler_Location_BadNameFallsBackToLocal pins the "operator
// typo'd the zone name" path: ventd must not refuse to schedule;
// instead Location() returns Local + ok=false so the caller can WARN.
// Per RULE-SCHEDULE-TZ-01.
func TestScheduler_Location_BadNameFallsBackToLocal(t *testing.T) {
	prev := loadLocationFn
	t.Cleanup(func() { loadLocationFn = prev })
	loadLocationFn = func(name string) (*time.Location, error) {
		return nil, errors.New("unknown time zone " + name)
	}

	sc := Scheduler{Timezone: "Atlantis/Lost"}
	loc, ok := sc.Location()
	if ok {
		t.Fatalf("Location() ok=true for bogus zone; want false so caller can WARN")
	}
	if loc != time.Local {
		t.Errorf("Location() fallback = %v, want time.Local", loc)
	}
}
