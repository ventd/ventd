package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/ebusy"
	"github.com/ventd/ventd/internal/hal/hwmon"
)

// ebusyStormFact returns the ebusy_storm fact from a doctor report, or nil.
func ebusyStormFact(r doctor.Report) *doctor.Fact {
	for i := range r.Facts {
		if r.Facts[i].Detector == "ebusy_storm" {
			return &r.Facts[i]
		}
	}
	return nil
}

func runDoctor(t *testing.T, srv *Server) doctor.Report {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/doctor", nil)
	w := httptest.NewRecorder()
	srv.handleDoctorReport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("doctor status=%d body=%q", w.Code, w.Body.String())
	}
	var report doctor.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal doctor report: %v", err)
	}
	return report
}

// TestDoctorReport_SurfacesActiveEBUSYStorm is the end-to-end wire for #1489:
// once a collector holding an above-threshold, currently-active storm is set on
// the server, GET /api/v1/doctor surfaces a Warning ebusy_storm fact naming the
// contested channel. Proves the web closure (s.ebusy → ebusy_storm detector)
// the unit tests could not reach.
func TestDoctorReport_SurfacesActiveEBUSYStorm(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	c := ebusy.New()
	c.Observe(hwmon.EBUSYRate{
		PWMPath:       "/sys/class/hwmon/hwmon9/pwm1",
		EventCount:    7, // above the 5-event storm threshold
		WindowStart:   time.Now().Unix(),
		WindowSeconds: 60,
	})
	srv.SetEBUSYCollector(c)

	got := ebusyStormFact(runDoctor(t, srv))
	if got == nil {
		t.Fatal("ebusy_storm fact missing from doctor report despite an active storm")
	}
	if got.Severity.String() != "warning" {
		t.Errorf("ebusy_storm severity = %q, want warning", got.Severity.String())
	}
	if !strings.Contains(got.Detail, "pwm1") {
		t.Errorf("ebusy_storm detail should name the contested channel; got: %s", got.Detail)
	}
}

// TestDoctorReport_NoEBUSYStormWhenQuiet pins the silent side: a server with no
// collector wired (monitor-only), and one whose only storm is below the
// threshold, both emit no ebusy_storm fact — a fresh server each time so the
// 5s report cache doesn't carry a verdict over.
func TestDoctorReport_NoEBUSYStormWhenQuiet(t *testing.T) {
	t.Run("no collector", func(t *testing.T) {
		srv, _, cancel := newHandlerHarness(t)
		defer cancel()
		if f := ebusyStormFact(runDoctor(t, srv)); f != nil {
			t.Errorf("ebusy_storm fact present with no collector wired: %+v", f)
		}
	})
	t.Run("below threshold", func(t *testing.T) {
		srv, _, cancel := newHandlerHarness(t)
		defer cancel()
		c := ebusy.New()
		c.Observe(hwmon.EBUSYRate{
			PWMPath:       "/sys/class/hwmon/hwmon9/pwm1",
			EventCount:    2, // benign one-off re-acquire, below threshold
			WindowStart:   time.Now().Unix(),
			WindowSeconds: 60,
		})
		srv.SetEBUSYCollector(c)
		if f := ebusyStormFact(runDoctor(t, srv)); f != nil {
			t.Errorf("ebusy_storm fact present for a below-threshold one-off: %+v", f)
		}
	})
}
