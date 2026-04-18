package hwmon

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// testModulesRoot overrides the default /lib/modules/<release> root in tests.
var testModulesRoot string

// modulesRootFor returns the modules directory root for the given kernel release.
// Tests set testModulesRoot to redirect file reads to a temp directory.
func modulesRootFor(krelease string) string {
	if testModulesRoot != "" {
		return testModulesRoot
	}
	return "/lib/modules/" + krelease
}

// parseModulesAlias parses a modules.alias reader and returns map[module][]aliases.
// Format: "alias <pattern> <module>" per line; "#" lines are comments.
// Malformed lines are logged and skipped — the function never returns a fatal error
// so a partial or stale alias file degrades gracefully.
func parseModulesAlias(r io.Reader) (map[string][]string, error) {
	result := make(map[string][]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "alias" {
			slog.Warn("modules.alias: skipping malformed line", "line", line)
			continue
		}
		pattern, module := fields[1], fields[2]
		result[module] = append(result[module], pattern)
	}
	return result, scanner.Err()
}

// loadModulesAlias reads and parses the modules.alias file from root.
// Returns nil on any error so callers can proceed without alias filtering.
func loadModulesAlias(root string, logger *slog.Logger) map[string][]string {
	path := filepath.Join(root, "modules.alias")
	f, err := os.Open(path)
	if err != nil {
		logger.Debug("modules.alias unavailable, alias filter disabled", "path", path, "err", err)
		return nil
	}
	defer func() { _ = f.Close() }()
	m, err := parseModulesAlias(f)
	if err != nil {
		logger.Debug("modules.alias parse error, alias filter disabled", "path", path, "err", err)
		return nil
	}
	return m
}
