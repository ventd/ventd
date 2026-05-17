package conflicts

import (
	"os"
	"path/filepath"
	"strings"
)

// detectProc scans procRoot/*/comm + cmdline for each entry's
// ProcPatterns. Returns a partial conflict report (UnitsActive etc.
// untouched). procRoot is /proc in production, fixture root in tests.
//
// "comm" is the kernel-truncated 15-character process name; "cmdline"
// is the full NUL-separated argv. Some daemons (Python wrappers,
// liquidctl one-shots) only match via cmdline because comm is "python3".
// Both surfaces are scanned and the first match per entry wins.
func detectProc(procRoot string, entries []Entry) map[string]*Conflict {
	out := make(map[string]*Conflict, len(entries))
	if procRoot == "" {
		return out
	}

	dirs, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}

	// Pre-collect (pid, comm, cmdline) so we make one pass over /proc.
	type procInfo struct {
		pid     string
		comm    string
		cmdline string
	}
	procs := make([]procInfo, 0, 64)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		if !isAllDigits(name) {
			continue
		}
		info := procInfo{pid: name}
		if b, err := os.ReadFile(filepath.Join(procRoot, name, "comm")); err == nil {
			info.comm = strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile(filepath.Join(procRoot, name, "cmdline")); err == nil {
			// cmdline is NUL-separated; replace with spaces for regex matching.
			info.cmdline = strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
		}
		if info.comm == "" && info.cmdline == "" {
			continue
		}
		procs = append(procs, info)
	}

	for _, e := range entries {
		if len(e.ProcPatterns) == 0 {
			continue
		}
		seen := make(map[string]struct{}, 4)
		for _, p := range procs {
			for _, pat := range e.ProcPatterns {
				if pat.MatchString(p.comm) || pat.MatchString(p.cmdline) {
					label := p.comm
					if label == "" {
						label = p.cmdline
					}
					key := p.pid + ":" + label
					if _, dup := seen[key]; dup {
						continue
					}
					seen[key] = struct{}{}
					c := out[e.Name]
					if c == nil {
						c = &Conflict{Entry: e}
						out[e.Name] = c
					}
					c.ProcessesFound = append(c.ProcessesFound, key)
					break
				}
			}
		}
	}
	return out
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
