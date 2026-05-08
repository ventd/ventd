package doctor

import (
	"encoding/json"
	"testing"
)

// TestSeverity_MarshalJSON pins the v0.5.27 fix: Severity MUST marshal
// as the lowercase string token, not as a uint8 integer. RULE-DOCTOR-08
// promised a schema-versioned JSON output; without MarshalJSON the
// wire format silently drifted to integers. Caught live on Phoenix's
// HIL when /doctor crashed with `(f.severity || "ok").toLowerCase is
// not a function`.
func TestSeverity_MarshalJSON(t *testing.T) {
	cases := []struct {
		s    Severity
		want string
	}{
		{SeverityOK, `"ok"`},
		{SeverityWarning, `"warning"`},
		{SeverityBlocker, `"blocker"`},
		{Severity(99), `"unknown"`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.s)
		if err != nil {
			t.Fatalf("Severity(%d) marshal: %v", c.s, err)
		}
		if string(got) != c.want {
			t.Errorf("Severity(%d) JSON = %s, want %s", c.s, got, c.want)
		}
	}
}

// TestSeverity_MarshalJSON_NestedInStruct guards against the regression
// where Severity was an unnamed uint8 field — Go's json package would
// emit the integer despite a String() method, because String() doesn't
// participate in JSON encoding. Pin that the MarshalJSON path fires
// even when Severity is embedded in another struct (the actual
// doctor.Fact / doctor.Report case).
func TestSeverity_MarshalJSON_NestedInStruct(t *testing.T) {
	type wrapper struct {
		Severity Severity `json:"severity"`
	}
	got, err := json.Marshal(wrapper{Severity: SeverityWarning})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"severity":"warning"}`
	if string(got) != want {
		t.Errorf("nested = %s, want %s", got, want)
	}
}

func TestSeverity_String(t *testing.T) {
	cases := []struct {
		s    Severity
		want string
	}{
		{SeverityOK, "ok"},
		{SeverityWarning, "warning"},
		{SeverityBlocker, "blocker"},
		{Severity(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Severity(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestSeverity_Worse(t *testing.T) {
	cases := []struct {
		a, b, want Severity
	}{
		{SeverityOK, SeverityOK, SeverityOK},
		{SeverityOK, SeverityWarning, SeverityWarning},
		{SeverityWarning, SeverityOK, SeverityWarning},
		{SeverityWarning, SeverityBlocker, SeverityBlocker},
		{SeverityBlocker, SeverityWarning, SeverityBlocker},
		{SeverityBlocker, SeverityBlocker, SeverityBlocker},
	}
	for _, c := range cases {
		if got := Worse(c.a, c.b); got != c.want {
			t.Errorf("Worse(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSeverity_ExitCode(t *testing.T) {
	// RULE-DOCTOR-02: exit codes are stable.
	cases := []struct {
		s    Severity
		want int
	}{
		{SeverityOK, 0},
		{SeverityWarning, 1},
		{SeverityBlocker, 2},
		{Severity(99), 3}, // unknown maps to "doctor itself errored"
	}
	for _, c := range cases {
		if got := c.s.ExitCode(); got != c.want {
			t.Errorf("Severity(%d).ExitCode() = %d, want %d", c.s, got, c.want)
		}
	}
}
