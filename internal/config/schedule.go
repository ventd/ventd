package config

import (
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// Schedule describes when a Profile is a candidate for scheduled
// activation. Parsed from the `schedule:` string on a Profile via
// ParseSchedule — see that function's godoc for the grammar.
//
// The zero value never matches (Days==0, StartMin==EndMin), so callers
// can treat an un-parsed Schedule as "always miss" without a nil guard.
type Schedule struct {
	// StartMin and EndMin are minutes-since-midnight in local time,
	// each in the range 0..1439. When StartMin < EndMin the window is
	// [StartMin, EndMin) on a single calendar day. When StartMin >
	// EndMin the window wraps midnight: [StartMin, 1440) on day D
	// plus [0, EndMin) on day D+1. StartMin == EndMin is rejected at
	// parse time — a zero-width window never matches and is almost
	// always an operator typo.
	StartMin int
	EndMin   int

	// Days is a 7-bit mask keyed by time.Weekday: bit 0 = Sunday,
	// bit 1 = Monday, …, bit 6 = Saturday. For wrapping time ranges
	// the day bit applies to the *start* calendar day: "22:00-07:00
	// mon" matches from 10pm Monday local to 7am Tuesday local.
	Days uint8
}

// allDaysMask covers Sunday..Saturday. Bit layout matches
// time.Weekday so Days&(1<<weekday) is a direct match check.
const allDaysMask uint8 = 0x7F

// dayBit maps a time.Weekday to its slot in the Days bitmask.
func dayBit(d time.Weekday) uint8 { return 1 << uint(d) }

// weekdayNames are the short lowercase forms accepted by ParseSchedule.
// Intentionally limited to the three-letter abbreviations: full names
// ("monday") would add parsing surface for no operational benefit, and
// mixed support is confusing.
var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday,
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
}

// ParseSchedule parses a schedule string into a Schedule. The grammar is
// space-separated:
//
//	"HH:MM-HH:MM DAYSPEC"
//
// where DAYSPEC is one of:
//
//	"*"            — every day
//	"mon-fri"      — an inclusive range (wraps: "fri-mon" = Fri,Sat,Sun,Mon)
//	"mon,wed,fri"  — a comma-separated list
//	"mon"          — a single day
//
// Time ranges may cross midnight: "22:00-07:00" is active from 22:00
// local on the matching day until 07:00 local the following day. Start
// == end is rejected.
//
// Empty input returns an error — callers that treat an absent schedule
// as "manual only" must check for "" before invoking ParseSchedule.
func ParseSchedule(s string) (*Schedule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("schedule: empty string")
	}
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return nil, fmt.Errorf("schedule: want 'HH:MM-HH:MM DAYSPEC', got %q", s)
	}
	startMin, endMin, err := parseTimeRange(parts[0])
	if err != nil {
		return nil, err
	}
	days, err := parseDaySpec(parts[1])
	if err != nil {
		return nil, err
	}
	return &Schedule{StartMin: startMin, EndMin: endMin, Days: days}, nil
}

func parseTimeRange(r string) (int, int, error) {
	dash := strings.Index(r, "-")
	if dash < 0 {
		return 0, 0, fmt.Errorf("schedule: time range %q must contain '-'", r)
	}
	start, err := parseHHMM(r[:dash])
	if err != nil {
		return 0, 0, err
	}
	end, err := parseHHMM(r[dash+1:])
	if err != nil {
		return 0, 0, err
	}
	if start == end {
		return 0, 0, fmt.Errorf("schedule: start == end in %q; zero-duration schedule never matches", r)
	}
	return start, end, nil
}

func parseHHMM(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("schedule: time %q must be HH:MM", s)
	}
	h, err := strconv.Atoi(s[:2])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("schedule: hour in %q must be 00-23", s)
	}
	m, err := strconv.Atoi(s[3:])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("schedule: minute in %q must be 00-59", s)
	}
	return h*60 + m, nil
}

func parseDaySpec(s string) (uint8, error) {
	if s == "*" {
		return allDaysMask, nil
	}
	if strings.Contains(s, ",") {
		var mask uint8
		for _, name := range strings.Split(s, ",") {
			d, err := lookupWeekday(name)
			if err != nil {
				return 0, err
			}
			mask |= dayBit(d)
		}
		if mask == 0 {
			return 0, fmt.Errorf("schedule: day list %q expanded to empty mask", s)
		}
		return mask, nil
	}
	if strings.Contains(s, "-") {
		parts := strings.SplitN(s, "-", 2)
		start, err := lookupWeekday(parts[0])
		if err != nil {
			return 0, err
		}
		end, err := lookupWeekday(parts[1])
		if err != nil {
			return 0, err
		}
		var mask uint8
		// Inclusive range with wrap support: "fri-mon" = Fri,Sat,Sun,Mon.
		// Modulo-7 walk terminates after at most 7 iterations because
		// each weekday value is distinct.
		for d := start; ; d = (d + 1) % 7 {
			mask |= dayBit(d)
			if d == end {
				break
			}
		}
		return mask, nil
	}
	d, err := lookupWeekday(s)
	if err != nil {
		return 0, err
	}
	return dayBit(d), nil
}

func lookupWeekday(name string) (time.Weekday, error) {
	d, ok := weekdayNames[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return 0, fmt.Errorf("schedule: unknown day %q (want sun/mon/tue/wed/thu/fri/sat)", name)
	}
	return d, nil
}

// Matches reports whether t falls within the schedule window. Times are
// evaluated in t's own location (time.Local for the scheduler's call
// path). Day attribution follows the start boundary: "22:00-07:00 mon"
// matches 23:00 Monday and 03:00 Tuesday, but not 23:00 Tuesday.
func (s *Schedule) Matches(t time.Time) bool {
	if s == nil || s.Days == 0 {
		return false
	}
	hm := t.Hour()*60 + t.Minute()
	today := dayBit(t.Weekday())
	if s.StartMin < s.EndMin {
		if hm < s.StartMin || hm >= s.EndMin {
			return false
		}
		return s.Days&today != 0
	}
	// Midnight-wrap: start > end.
	if hm >= s.StartMin {
		return s.Days&today != 0
	}
	if hm < s.EndMin {
		// Attribute the early-morning half to yesterday's weekday so
		// the day bit lines up with the schedule's start boundary.
		yesterday := dayBit(((t.Weekday() + 6) % 7))
		return s.Days&yesterday != 0
	}
	return false
}

// DurationMin returns how many minutes per active day the window covers.
// Non-wrapping schedules return End-Start; wrapping schedules return
// (1440-Start)+End. Used by the scheduler's specificity tiebreak — a
// shorter window is treated as more specific than a longer one that
// overlaps it.
func (s *Schedule) DurationMin() int {
	if s == nil {
		return 0
	}
	if s.StartMin < s.EndMin {
		return s.EndMin - s.StartMin
	}
	return (1440 - s.StartMin) + s.EndMin
}

// DayCount returns how many weekdays this schedule covers.
func (s *Schedule) DayCount() int {
	if s == nil {
		return 0
	}
	return bits.OnesCount8(s.Days)
}
