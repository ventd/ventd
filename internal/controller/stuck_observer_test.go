package controller

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// TestReadAllSensors_ObserverReceivesPlausibleTempReadings proves the
// stuck-sensor freeze hook (RULE-DOCTOR-DETECTOR-STUCK-SENSOR) is wired to the
// real sensor read: the observer fires for plausible hwmon temp readings with
// the raw value and tick time, and is deliberately NOT called for sentinel /
// implausibly-low temps (already surfaced elsewhere), non-temp hwmon paths, or
// nvidia sensors (which self-validate).
func TestReadAllSensors_ObserverReceivesPlausibleTempReadings(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	healthy := mk("temp1_input", "45000")     // 45.0°C — plausible, must be observed
	disconnected := mk("temp2_input", "8500") // 8.5°C — low/disconnected, must NOT be observed
	voltage := mk("in0_input", "3300")        // non-temp path, must NOT be observed

	sensors := []config.Sensor{
		{Name: "cpu", Type: "hwmon", Path: healthy},
		{Name: "dead", Type: "hwmon", Path: disconnected},
		{Name: "vcore", Type: "hwmon", Path: voltage},
	}

	got := map[string]obsRec{}
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	observer := func(name string, valueC float64, at time.Time) {
		got[name] = obsRec{valueC, at}
	}

	dst := map[string]float64{}
	sentinel := map[string]bool{}
	readAllSensors(slog.New(slog.NewTextHandler(io.Discard, nil)), sensors, dst, sentinel, observer, now)

	if len(got) != 1 {
		t.Fatalf("observer fired for %v; want only the plausible temp sensor", keysOf(got))
	}
	o, ok := got["cpu"]
	if !ok {
		t.Fatal("plausible temp sensor cpu was not observed")
	}
	if o.val != 45.0 {
		t.Errorf("observed value = %v, want raw 45.0", o.val)
	}
	if !o.at.Equal(now) {
		t.Errorf("observed time = %v, want tick time %v", o.at, now)
	}
}

type obsRec struct {
	val float64
	at  time.Time
}

func keysOf(m map[string]obsRec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
