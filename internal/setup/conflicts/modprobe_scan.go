package conflicts

import (
	"os"
	"path/filepath"
)

// modprobeDirs are the two locations the detector scans for drop-in
// files. modprobe.d carries `options <module> <args>` lines that
// change module behaviour; modules-load.d carries one-module-per-line
// auto-load lists. Both are evidence that a competitor's package is
// installed and configured to take effect at boot.
var modprobeDirs = []string{"/etc/modprobe.d", "/etc/modules-load.d"}

// detectModprobeDropIns checks each registry entry's ModprobeDropIns
// against modprobeDirs. Reports a conflict when any declared basename
// exists in either directory. The check is filename-presence, not
// content-parsing — the wizard's job is to alert; the operator decides
// whether the drop-in's content actually conflicts.
//
// dirs override is for tests; nil uses the production paths.
func detectModprobeDropIns(dirs []string, entries []Entry) map[string]*Conflict {
	out := make(map[string]*Conflict, len(entries))
	if dirs == nil {
		dirs = modprobeDirs
	}
	for _, e := range entries {
		if len(e.ModprobeDropIns) == 0 {
			continue
		}
		for _, basename := range e.ModprobeDropIns {
			for _, dir := range dirs {
				path := filepath.Join(dir, basename)
				if _, err := os.Stat(path); err == nil {
					c := out[e.Name]
					if c == nil {
						c = &Conflict{Entry: e}
						out[e.Name] = c
					}
					// Deduplicate — same basename present in
					// both dirs is reported once.
					if !contains(c.ModprobeFound, path) {
						c.ModprobeFound = append(c.ModprobeFound, path)
					}
				}
			}
		}
	}
	return out
}

// detectConfigPaths checks each registry entry's ConfigPaths for
// existence. Files OR directories count. This catches daemons that are
// installed but not currently running — the wizard's "fancontrol is
// installed; did you mean to enable it?" UX.
func detectConfigPaths(entries []Entry) map[string]*Conflict {
	out := make(map[string]*Conflict, len(entries))
	for _, e := range entries {
		if len(e.ConfigPaths) == 0 {
			continue
		}
		for _, p := range e.ConfigPaths {
			if _, err := os.Stat(p); err == nil {
				c := out[e.Name]
				if c == nil {
					c = &Conflict{Entry: e}
					out[e.Name] = c
				}
				c.ConfigsFound = append(c.ConfigsFound, p)
			}
		}
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
