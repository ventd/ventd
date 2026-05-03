package doctor

import "testing"

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
