package ebusy_test

import (
	"testing"
	"time"

	"github.com/ventd/ventd/internal/ebusy"
	"github.com/ventd/ventd/internal/hal/hwmon"
)

func TestCollector_ActiveStormsFiltersStaleAndSorts(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	c := ebusy.New()

	// Two active windows (started within the 60s span) + one stale (started
	// 120s ago, past the window). Observe out of path order to check sorting.
	c.Observe(hwmon.EBUSYRate{PWMPath: "/sys/.../pwm3", EventCount: 9, WindowStart: now.Unix() - 5, WindowSeconds: 60})
	c.Observe(hwmon.EBUSYRate{PWMPath: "/sys/.../pwm1", EventCount: 7, WindowStart: now.Unix() - 10, WindowSeconds: 60})
	c.Observe(hwmon.EBUSYRate{PWMPath: "/sys/.../pwm9", EventCount: 30, WindowStart: now.Unix() - 120, WindowSeconds: 60}) // stale

	got := c.ActiveStorms(now)
	if len(got) != 2 {
		t.Fatalf("active storms = %d, want 2 (stale window dropped)", len(got))
	}
	if got[0].PWMPath != "/sys/.../pwm1" || got[1].PWMPath != "/sys/.../pwm3" {
		t.Errorf("not sorted by path: %q, %q", got[0].PWMPath, got[1].PWMPath)
	}
}

func TestCollector_LatestSnapshotPerChannelWins(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	c := ebusy.New()
	c.Observe(hwmon.EBUSYRate{PWMPath: "/p", EventCount: 1, WindowStart: now.Unix(), WindowSeconds: 60})
	c.Observe(hwmon.EBUSYRate{PWMPath: "/p", EventCount: 6, WindowStart: now.Unix(), WindowSeconds: 60})
	got := c.ActiveStorms(now)
	if len(got) != 1 || got[0].EventCount != 6 {
		t.Fatalf("want one channel with latest count 6, got %+v", got)
	}
}

func TestCollector_NilSafe(t *testing.T) {
	var c *ebusy.Collector
	c.Observe(hwmon.EBUSYRate{PWMPath: "/p", EventCount: 9, WindowStart: 1, WindowSeconds: 60}) // must not panic
	if got := c.ActiveStorms(time.Unix(2, 0)); got != nil {
		t.Errorf("nil collector ActiveStorms = %v, want nil", got)
	}
}
