package detection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/hwdb"
)

const (
	calibrationDir = "/var/lib/ventd/calibration"
	ventdEtcDir    = "/etc/ventd"
)

// CollectState gathers ventd's own runtime state (§12.6).
// Imports internal/hwdb for CalibrationRun types — does NOT import internal/calibration.
func CollectState(ctx context.Context) CollectResult {
	var res CollectResult
	add := func(item Item) { res.Items = append(res.Items, item) }

	// Calibration JSON files — decode via hwdb types to validate, write originals.
	calibItems, calSymlink := collectCalibration()
	res.Items = append(res.Items, calibItems...)
	if calSymlink.Path != "" {
		add(calSymlink)
	}

	// ventd config files under /etc/ventd/
	walkEtcVentd(&res)

	// Version info (populated by the CLI caller, stored as a simple file).
	add(textItem("commands/ventd/version", ventdVersionBytes()))

	return res
}

func collectCalibration() ([]Item, Item) {
	var items []Item
	var bestSymlink Item

	entries, err := os.ReadDir(calibrationDir)
	if err != nil {
		return nil, bestSymlink
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(calibrationDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Validate that it decodes as a CalibrationRun.
		var run hwdb.CalibrationRun
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		bundlePath := "state/calibration/" + e.Name()
		items = append(items, Item{Path: bundlePath, Content: data})
		// Use the most recently calibrated file for the top-level symlink.
		if bestSymlink.Path == "" || run.CalibratedAt.After(bestCalTime(items)) {
			bestSymlink = symlinkItem("calibration", bundlePath)
		}
	}
	return items, bestSymlink
}

func bestCalTime(items []Item) time.Time {
	var latest time.Time
	for _, it := range items {
		var run hwdb.CalibrationRun
		if err := json.Unmarshal(it.Content, &run); err == nil {
			if run.CalibratedAt.After(latest) {
				latest = run.CalibratedAt
			}
		}
	}
	return latest
}

func walkEtcVentd(res *CollectResult) {
	entries, err := os.ReadDir(ventdEtcDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data := readFile(filepath.Join(ventdEtcDir, e.Name()))
		if len(data) > 0 {
			res.Items = append(res.Items, Item{
				Path:    "etc/ventd/" + e.Name(),
				Content: data,
			})
		}
	}
	// Profile symlink to active config if it exists.
	if _, err := os.Stat(filepath.Join(ventdEtcDir, "config.yaml")); err == nil {
		res.Items = append(res.Items, symlinkItem("profile", "etc/ventd/config.yaml"))
	}
}

func ventdVersionBytes() []byte {
	return fmt.Appendf(nil, "ventd (diagnostic bundle capture)\ncaptured_at: %s\n",
		time.Now().UTC().Format(time.RFC3339))
}
