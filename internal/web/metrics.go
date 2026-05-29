// Package web — Prometheus/OpenMetrics text-exposition endpoint.
//
// /metrics renders the daemon's live state in the Prometheus text format so a
// homelab Prometheus/VictoriaMetrics/Grafana-Agent scrape works out of the box
// — the #1 monitoring-integration gap. It is hand-rolled rather than pulling in
// client_golang: ventd is a single CGO-free static binary and the exposition
// format is a few lines of text. Every sample is sourced from buildStatus() —
// the same live snapshot /api/v1/status serves — so a metric never reports a
// value the daemon isn't actually reading (no fabricated readings).
package web

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// metricsContentType is the Prometheus text exposition format, version 0.0.4.
const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

// handleMetrics serves GET /metrics. Unauthenticated by design — operational
// telemetry with the same posture as /healthz and /readyz (no secrets), so a
// scraper needs no credentials. RULE-WEB-METRICS-EXPOSITION.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", metricsContentType)
	w.Header().Set("Cache-Control", "no-store")
	writeMetrics(w, s.buildStatus(), s.version.Version)
}

// errWriter accumulates the first write error so the exposition builder can
// stream line-by-line without an error check on every Fprintf (and satisfies
// errcheck). A write failure — typically the scraper hanging up mid-response —
// short-circuits the rest; the handler doesn't surface it (net/http logging
// covers a dropped client).
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, a...)
}

// writeMetrics renders the Prometheus text exposition for a status snapshot.
// Pure (writes only to w) so the format is unit-testable without a live daemon.
// Each metric family emits its HELP/TYPE header exactly once and only when it
// has at least one sample, so a daemon with no tach-capable fans simply omits
// ventd_fan_rpm rather than emitting a header with no series.
func writeMetrics(w io.Writer, st statusResponse, version string) {
	ew := &errWriter{w: w}

	ew.printf("# HELP ventd_up 1 when the ventd daemon is up and serving.\n")
	ew.printf("# TYPE ventd_up gauge\n")
	ew.printf("ventd_up 1\n")

	if version != "" {
		ew.printf("# HELP ventd_build_info Build metadata; constant 1 carrying the version label.\n")
		ew.printf("# TYPE ventd_build_info gauge\n")
		ew.printf("ventd_build_info{version=\"%s\"} 1\n", escapeLabelValue(version))
	}

	if !st.StartedAt.IsZero() {
		ew.printf("# HELP ventd_start_time_seconds Unix start time of the daemon, in seconds.\n")
		ew.printf("# TYPE ventd_start_time_seconds gauge\n")
		ew.printf("ventd_start_time_seconds %s\n",
			strconv.FormatFloat(float64(st.StartedAt.UnixNano())/1e9, 'f', -1, 64))
	}

	ew.printf("# HELP ventd_shadow_mode 1 when running in shadow mode (no hardware writes are issued).\n")
	ew.printf("# TYPE ventd_shadow_mode gauge\n")
	ew.printf("ventd_shadow_mode %d\n", boolToInt(st.ShadowMode))

	// Temperature sensors (°C). Sentinel/failed reads have a nil Value and are
	// omitted — a scraped gauge is always a reading the daemon trusts.
	tempHeader := false
	for _, sn := range st.Sensors {
		if sn.Value == nil || sn.Unit != "°C" {
			continue
		}
		if !tempHeader {
			ew.printf("# HELP ventd_sensor_temperature_celsius Current sensor temperature in degrees Celsius.\n")
			ew.printf("# TYPE ventd_sensor_temperature_celsius gauge\n")
			tempHeader = true
		}
		ew.printf("ventd_sensor_temperature_celsius{sensor=\"%s\"} %s\n",
			escapeLabelValue(sn.Name), formatMetricFloat(*sn.Value))
	}

	// Non-temperature sensors (%, W, V, MHz, …) — unit carried in a label so
	// nothing is dropped or mislabeled as a temperature.
	genHeader := false
	for _, sn := range st.Sensors {
		if sn.Value == nil || sn.Unit == "°C" {
			continue
		}
		if !genHeader {
			ew.printf("# HELP ventd_sensor_value Current non-temperature sensor reading; physical unit in the unit label.\n")
			ew.printf("# TYPE ventd_sensor_value gauge\n")
			genHeader = true
		}
		ew.printf("ventd_sensor_value{sensor=\"%s\",unit=\"%s\"} %s\n",
			escapeLabelValue(sn.Name), escapeLabelValue(sn.Unit), formatMetricFloat(*sn.Value))
	}

	if len(st.Fans) > 0 {
		ew.printf("# HELP ventd_fan_pwm Current fan PWM duty byte (0-255).\n")
		ew.printf("# TYPE ventd_fan_pwm gauge\n")
		for _, f := range st.Fans {
			ew.printf("ventd_fan_pwm{fan=\"%s\",label=\"%s\"} %d\n",
				escapeLabelValue(f.Name), escapeLabelValue(f.Label), f.PWM)
		}
	}

	// Tach RPM — only for fans that report it (nil RPM → "—" in the UI, omitted
	// here rather than emitting a fabricated 0).
	rpmHeader := false
	for _, f := range st.Fans {
		if f.RPM == nil {
			continue
		}
		if !rpmHeader {
			ew.printf("# HELP ventd_fan_rpm Current fan tachometer reading, in RPM.\n")
			ew.printf("# TYPE ventd_fan_rpm gauge\n")
			rpmHeader = true
		}
		ew.printf("ventd_fan_rpm{fan=\"%s\",label=\"%s\"} %d\n",
			escapeLabelValue(f.Name), escapeLabelValue(f.Label), *f.RPM)
	}
}

// escapeLabelValue escapes a Prometheus label value per the exposition spec:
// backslash, double-quote, and newline only (not Go's wider %q set).
func escapeLabelValue(v string) string {
	if !strings.ContainsAny(v, "\\\"\n") {
		return v
	}
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// formatMetricFloat renders a float as a compact, Prometheus-valid sample value.
func formatMetricFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
