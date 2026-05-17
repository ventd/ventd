package conflicts

import (
	"context"
	"sort"
)

// DetectOptions configures the multi-modal scan.
type DetectOptions struct {
	// Systemctl is the runner used for unit-state queries. nil
	// disables systemd detection (e.g. tests on a non-systemd host).
	Systemctl SystemctlRunner

	// ProcRoot is /proc in production, fixture root in tests. Empty
	// disables proc + fd detection.
	ProcRoot string

	// HwmonRoot is /sys/class/hwmon in production. Empty disables
	// the fd-holder safety net.
	HwmonRoot string

	// ModprobeDirs overrides the production /etc/modprobe.d and
	// /etc/modules-load.d paths. nil uses production.
	ModprobeDirs []string

	// Entries overrides the global Registry. nil uses Registry.
	// Tests inject a minimal entry set for focused assertions.
	Entries []Entry

	// DisableConfigPathCheck skips the ConfigPaths probe (the
	// "fancontrol is installed but inactive" signal). Off by default;
	// tests use it when they only want to assert one detection mode.
	DisableConfigPathCheck bool
}

// Detect runs every registered detection helper and merges the
// per-entry results. Returns conflicts sorted by Intrusiveness ascending
// then Name so the wizard's recovery card lists cheap stops first and
// scary vendor daemons last.
//
// All detection helpers are best-effort: a single failing helper (e.g.
// systemctl missing inside a container) does not block the others.
// The returned slice may be empty — that's the happy path.
func Detect(ctx context.Context, opts DetectOptions) []Conflict {
	entries := opts.Entries
	if entries == nil {
		entries = Registry
	}

	merged := make(map[string]*Conflict, len(entries))

	mergeInto := func(partial map[string]*Conflict) {
		for name, c := range partial {
			if c == nil || !c.HasSignal() {
				continue
			}
			existing := merged[name]
			if existing == nil {
				merged[name] = c
				continue
			}
			existing.UnitsActive = mergeStrings(existing.UnitsActive, c.UnitsActive)
			existing.UnitsEnabled = mergeStrings(existing.UnitsEnabled, c.UnitsEnabled)
			existing.ProcessesFound = mergeStrings(existing.ProcessesFound, c.ProcessesFound)
			existing.ConfigsFound = mergeStrings(existing.ConfigsFound, c.ConfigsFound)
			existing.ModprobeFound = mergeStrings(existing.ModprobeFound, c.ModprobeFound)
			existing.FDHolders = mergeStrings(existing.FDHolders, c.FDHolders)
		}
	}

	mergeInto(detectSystemd(ctx, opts.Systemctl, entries))
	procResults := detectProc(opts.ProcRoot, entries)
	mergeInto(procResults)
	if !opts.DisableConfigPathCheck {
		mergeInto(detectConfigPaths(entries))
	}
	mergeInto(detectModprobeDropIns(opts.ModprobeDirs, entries))

	// Build a comm→entry index for the fd-holder attribution path.
	commIndex := make(map[string]*Conflict, len(procResults))
	for _, c := range procResults {
		for _, label := range c.ProcessesFound {
			// label format is "pid:comm[ -> target]" — extract comm
			parts := splitFirst(label, ":")
			if len(parts) == 2 {
				commIndex[parts[1]] = c
			}
		}
	}
	mergeInto(detectFDHolders(opts.ProcRoot, opts.HwmonRoot, commIndex))

	out := make([]Conflict, 0, len(merged))
	for _, c := range merged {
		if c != nil && c.HasSignal() {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entry.Intrusiveness != out[j].Entry.Intrusiveness {
			return out[i].Entry.Intrusiveness < out[j].Entry.Intrusiveness
		}
		return out[i].Entry.Name < out[j].Entry.Name
	})
	return out
}

func mergeStrings(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	out := append([]string{}, a...)
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// splitFirst splits s on the first occurrence of sep. If sep is not
// present, returns [s].
func splitFirst(s, sep string) []string {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return []string{s[:i], s[i+len(sep):]}
		}
	}
	return []string{s}
}
