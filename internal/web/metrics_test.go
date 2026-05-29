package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// RULE-WEB-METRICS-EXPOSITION: /metrics renders live state in Prometheus text
// format, sourced from buildStatus(); sentinel/unknown readings are omitted
// (no fabricated 0); the endpoint is GET-only and unauthenticated.

func ptrFloat(v float64) *float64 { return &v }
func ptrInt(v int) *int           { return &v }

func TestWriteMetrics_Format(t *testing.T) {
	t.Parallel()
	st := statusResponse{
		StartedAt:  time.Unix(1700000000, 0),
		ShadowMode: true,
		Sensors: []sensorStatus{
			{Name: "cpu", Value: ptrFloat(42.5), Unit: "°C"},
			{Name: "gpu", Value: nil, Unit: "°C"},              // sentinel/failed → omitted
			{Name: "gpu_util", Value: ptrFloat(73), Unit: "%"}, // generic
		},
		Fans: []fanStatus{
			{Name: "cpu fan", Label: "CPU Fan", PWM: 80, RPM: ptrInt(1129)},
			{Name: "case fan", Label: "Case", PWM: 120, RPM: nil}, // no tach → no rpm series
		},
	}
	var sb strings.Builder
	writeMetrics(&sb, st, "v1.3.0")
	out := sb.String()

	mustContain := []string{
		"# TYPE ventd_up gauge",
		"ventd_up 1",
		`ventd_build_info{version="v1.3.0"} 1`,
		"ventd_start_time_seconds 1700000000",
		"ventd_shadow_mode 1",
		"# TYPE ventd_sensor_temperature_celsius gauge",
		`ventd_sensor_temperature_celsius{sensor="cpu"} 42.5`,
		`ventd_sensor_value{sensor="gpu_util",unit="%"} 73`,
		"# TYPE ventd_fan_pwm gauge",
		`ventd_fan_pwm{fan="cpu fan",label="CPU Fan"} 80`,
		`ventd_fan_pwm{fan="case fan",label="Case"} 120`,
		"# TYPE ventd_fan_rpm gauge",
		`ventd_fan_rpm{fan="cpu fan",label="CPU Fan"} 1129`,
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("metrics output missing %q\n--- got ---\n%s", want, out)
		}
	}

	// Omissions: the nil-value temp sensor and the nil-RPM fan must NOT appear.
	mustNotContain := []string{
		`sensor="gpu"}`,                // gpu temp was nil
		`ventd_fan_rpm{fan="case fan"`, // case fan has no tach
	}
	for _, bad := range mustNotContain {
		if strings.Contains(out, bad) {
			t.Errorf("metrics output should omit %q (no fabricated reading)\n--- got ---\n%s", bad, out)
		}
	}

	// HELP/TYPE headers must appear exactly once per family.
	for _, family := range []string{"# TYPE ventd_fan_pwm gauge", "# TYPE ventd_sensor_temperature_celsius gauge"} {
		if n := strings.Count(out, family); n != 1 {
			t.Errorf("family header %q appeared %d times, want 1", family, n)
		}
	}
}

func TestWriteMetrics_OmitsEmptyFamiliesAndBlankVersion(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	writeMetrics(&sb, statusResponse{}, "") // no sensors, no fans, no version
	out := sb.String()
	if strings.Contains(out, "ventd_build_info") {
		t.Error("blank version must omit ventd_build_info")
	}
	for _, fam := range []string{"ventd_fan_pwm", "ventd_fan_rpm", "ventd_sensor_temperature_celsius", "ventd_start_time_seconds"} {
		if strings.Contains(out, fam) {
			t.Errorf("empty snapshot must omit %q", fam)
		}
	}
	if !strings.Contains(out, "ventd_up 1") {
		t.Error("ventd_up must always be present")
	}
}

func TestEscapeLabelValue(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"plain":        "plain",
		`a"b`:          `a\"b`,
		`a\b`:          `a\\b`,
		"line1\nline2": `line1\nline2`,
		"CHA_FAN1":     "CHA_FAN1",
	}
	for in, want := range cases {
		if got := escapeLabelValue(in); got != want {
			t.Errorf("escapeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleMetrics_GETOnly(t *testing.T) {
	t.Parallel()
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()
	s.handleMetrics(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow header = %q, want GET", got)
	}
}
