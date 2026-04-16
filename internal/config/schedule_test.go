package config

import (
	"testing"
	"time"
)

func TestParseSchedule_Valid(t *testing.T) {
	cases := []struct {
		in       string
		startMin int
		endMin   int
		days     uint8
		dayCount int
		durMin   int
	}{
		{
			in:       "22:00-07:00 *",
			startMin: 22 * 60,
			endMin:   7 * 60,
			days:     allDaysMask,
			dayCount: 7,
			durMin:   (1440 - 22*60) + 7*60,
		},
		{
			in:       "09:00-18:00 mon-fri",
			startMin: 9 * 60,
			endMin:   18 * 60,
			days:     dayBit(time.Monday) | dayBit(time.Tuesday) | dayBit(time.Wednesday) | dayBit(time.Thursday) | dayBit(time.Friday),
			dayCount: 5,
			durMin:   9 * 60,
		},
		{
			in:       "10:00-11:30 mon,wed,fri",
			startMin: 10 * 60,
			endMin:   11*60 + 30,
			days:     dayBit(time.Monday) | dayBit(time.Wednesday) | dayBit(time.Friday),
			dayCount: 3,
			durMin:   90,
		},
		{
			in:       "06:00-07:00 mon",
			startMin: 6 * 60,
			endMin:   7 * 60,
			days:     dayBit(time.Monday),
			dayCount: 1,
			durMin:   60,
		},
		{
			in:       "00:00-23:59 *",
			startMin: 0,
			endMin:   23*60 + 59,
			days:     allDaysMask,
			dayCount: 7,
			durMin:   23*60 + 59,
		},
		{
			// Wrap day range: Fri..Mon covers Fri, Sat, Sun, Mon.
			in:       "20:00-23:00 fri-mon",
			startMin: 20 * 60,
			endMin:   23 * 60,
			days:     dayBit(time.Friday) | dayBit(time.Saturday) | dayBit(time.Sunday) | dayBit(time.Monday),
			dayCount: 4,
			durMin:   3 * 60,
		},
		{
			// Case-insensitive day names tolerate YAML quirks.
			in:       "09:00-10:00 SAT,SUN",
			startMin: 9 * 60,
			endMin:   10 * 60,
			days:     dayBit(time.Saturday) | dayBit(time.Sunday),
			dayCount: 2,
			durMin:   60,
		},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			s, err := ParseSchedule(tc.in)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) err: %v", tc.in, err)
			}
			if s.StartMin != tc.startMin || s.EndMin != tc.endMin {
				t.Errorf("times = (%d,%d), want (%d,%d)", s.StartMin, s.EndMin, tc.startMin, tc.endMin)
			}
			if s.Days != tc.days {
				t.Errorf("days mask = 0x%02x, want 0x%02x", s.Days, tc.days)
			}
			if got := s.DayCount(); got != tc.dayCount {
				t.Errorf("DayCount = %d, want %d", got, tc.dayCount)
			}
			if got := s.DurationMin(); got != tc.durMin {
				t.Errorf("DurationMin = %d, want %d", got, tc.durMin)
			}
		})
	}
}

func TestParseSchedule_Invalid(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"22:00-07:00",      // missing day spec
		"22:00-07:00 * *",  // extra token
		"22:00 *",          // missing end time
		"24:00-07:00 *",    // hour out of range
		"22:00-23:60 *",    // minute out of range
		"22:0-07:00 *",     // wrong digit count
		"22:00-07:00 xyz",  // unknown day
		"22:00-07:00 mon-", // trailing dash
		"-07:00 *",         // no start time
		"22:00-22:00 *",    // zero duration
		"22:00-07:00 mon,", // trailing comma producing unknown day
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseSchedule(in); err == nil {
				t.Errorf("ParseSchedule(%q) accepted; want error", in)
			}
		})
	}
}

func TestSchedule_Matches_NonWrap(t *testing.T) {
	// 09:00-18:00 mon-fri, evaluated at Thursday 2026-01-15 times.
	s, err := ParseSchedule("09:00-18:00 mon-fri")
	if err != nil {
		t.Fatal(err)
	}
	loc := time.Local
	thu := func(h, m int) time.Time { return time.Date(2026, 1, 15, h, m, 0, 0, loc) }
	sat := func(h, m int) time.Time { return time.Date(2026, 1, 17, h, m, 0, 0, loc) }
	cases := []struct {
		t    time.Time
		want bool
	}{
		{thu(8, 59), false},
		{thu(9, 0), true},
		{thu(12, 0), true},
		{thu(17, 59), true},
		{thu(18, 0), false},
		{sat(12, 0), false}, // weekend
	}
	for _, c := range cases {
		if got := s.Matches(c.t); got != c.want {
			t.Errorf("Matches(%s) = %v want %v", c.t.Format(time.RFC3339), got, c.want)
		}
	}
}

func TestSchedule_Matches_Wrap(t *testing.T) {
	// 22:00-07:00 every day — includes the Mon 22:00..Tue 07:00 window.
	s, err := ParseSchedule("22:00-07:00 *")
	if err != nil {
		t.Fatal(err)
	}
	loc := time.Local
	mon := func(h, m int) time.Time { return time.Date(2026, 1, 12, h, m, 0, 0, loc) }
	tue := func(h, m int) time.Time { return time.Date(2026, 1, 13, h, m, 0, 0, loc) }
	cases := []struct {
		t    time.Time
		want bool
	}{
		{mon(21, 59), false},
		{mon(22, 0), true},
		{mon(23, 30), true},
		{tue(0, 0), true},
		{tue(6, 59), true},
		{tue(7, 0), false},
		{tue(12, 0), false},
	}
	for _, c := range cases {
		if got := s.Matches(c.t); got != c.want {
			t.Errorf("Matches(%s) = %v want %v", c.t.Format(time.RFC3339), got, c.want)
		}
	}
}

func TestSchedule_Matches_WrapDayAttribution(t *testing.T) {
	// "22:00-07:00 mon" should match Mon 22:00 and Tue 03:00 (spillover),
	// but NOT Tue 22:00 (Tuesday is not in the day mask).
	s, err := ParseSchedule("22:00-07:00 mon")
	if err != nil {
		t.Fatal(err)
	}
	loc := time.Local
	mon := func(h, m int) time.Time { return time.Date(2026, 1, 12, h, m, 0, 0, loc) }
	tue := func(h, m int) time.Time { return time.Date(2026, 1, 13, h, m, 0, 0, loc) }
	if !s.Matches(mon(22, 30)) {
		t.Errorf("Mon 22:30 should match")
	}
	if !s.Matches(tue(3, 0)) {
		t.Errorf("Tue 03:00 should match (Monday spillover)")
	}
	if s.Matches(tue(22, 30)) {
		t.Errorf("Tue 22:30 should NOT match (only Mon is in day mask)")
	}
}

func TestSchedule_NilAndZero_NeverMatch(t *testing.T) {
	var nilSched *Schedule
	if nilSched.Matches(time.Now()) {
		t.Errorf("nil schedule must not match")
	}
	var zero Schedule
	if zero.Matches(time.Now()) {
		t.Errorf("zero schedule must not match (empty day mask)")
	}
}

func TestSchedule_DurationAndDayCount_OnNilOrZero(t *testing.T) {
	var nilSched *Schedule
	if got := nilSched.DurationMin(); got != 0 {
		t.Errorf("nil DurationMin = %d want 0", got)
	}
	if got := nilSched.DayCount(); got != 0 {
		t.Errorf("nil DayCount = %d want 0", got)
	}
}
