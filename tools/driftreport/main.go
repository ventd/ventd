// Command driftreport measures code-quality metrics and flags regressions
// against a committed baseline. It exits 0 always; CI reads the Drifts
// field to decide whether to open a GitHub issue.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Thresholds controls when a metric change becomes a reported drift.
type Thresholds struct {
	CoverageRegressionPct float64 `yaml:"coverage_regression_pct"`
	BinarySizeDeltaKB     int     `yaml:"binary_size_delta_kb"`
	DupLOCIncrease        int     `yaml:"dup_loc_increase"`
	DeadCodeIncrease      int     `yaml:"dead_code_count_increase"`
	DepGraphSizePct       float64 `yaml:"dep_graph_size_pct"`
}

// Snapshot is one measurement of the repo's quality state.
type Snapshot struct {
	DeadCodeCount int                `json:"dead_code_count"`
	DupLOC        int                `json:"dup_loc"`
	Coverage      map[string]float64 `json:"coverage"`
	BinarySizeKB  int                `json:"binary_size_kb"`
	DepGraphLines int                `json:"dep_graph_lines"`
	HALImplCount  int                `json:"hal_impl_count"`
}

// Report is the full output: current state, baseline, and any drift violations.
type Report struct {
	Current  Snapshot `json:"current"`
	Baseline Snapshot `json:"baseline"`
	Drifts   []string `json:"drifts"`
}

var coverRE = regexp.MustCompile(`^(ok|FAIL)\s+(\S+)\s+.*coverage:\s+([\d.]+)%`)
var halMethodRE = regexp.MustCompile(`func \([^)]+\) (Enumerate|Read|Write|Restore|Close|Name)\(`)

func main() {
	var root, baselinePath, thresholdsPath, outputPath string
	var snapshotOnly bool
	flag.StringVar(&root, "root", ".", "repo root")
	flag.StringVar(&baselinePath, "baseline", ".github/drift-baseline.json", "baseline JSON path (relative to root)")
	flag.StringVar(&thresholdsPath, "thresholds", ".github/drift-thresholds.yaml", "thresholds YAML path (relative to root)")
	flag.StringVar(&outputPath, "output", "", "write report JSON to file (empty = stdout)")
	flag.BoolVar(&snapshotOnly, "snapshot", false, "output only the current Snapshot (for baseline update)")
	flag.Parse()

	cur := measure(root)

	if snapshotOnly {
		emit(outputPath, cur)
		return
	}

	thr := loadThresholds(filepath.Join(root, thresholdsPath))
	base := loadBaseline(filepath.Join(root, baselinePath))

	report := Report{
		Current:  cur,
		Baseline: base,
		Drifts:   computeDrifts(cur, base, thr),
	}
	for _, d := range report.Drifts {
		fmt.Fprintln(os.Stderr, "DRIFT:", d)
	}
	emit(outputPath, report)
}

func measure(root string) Snapshot {
	return Snapshot{
		DeadCodeCount: deadCodeCount(root),
		DupLOC:        dupLOC(root),
		Coverage:      coverage(root),
		BinarySizeKB:  binarySizeKB(root),
		DepGraphLines: depGraphLines(root),
		HALImplCount:  halImplCount(root),
	}
}

func deadCodeCount(root string) int {
	out, err := exec.Command("golangci-lint", "run",
		"--enable=unused,deadcode", "--out-format=json", "./...").
		CombinedOutput()
	if err != nil && len(out) == 0 {
		return -1 // tool absent
	}
	return strings.Count(string(out), `"Text"`)
}

func dupLOC(root string) int {
	out, err := exec.Command("dupl", "-t", "75", "-p", "./...").CombinedOutput()
	if err != nil && len(out) == 0 {
		return -1
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	lines := 0
	for sc.Scan() {
		lines++
	}
	return lines
}

func coverage(root string) map[string]float64 {
	result := make(map[string]float64)
	out, _ := exec.Command("go", "test", "-cover", "./...").CombinedOutput()
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if m := coverRE.FindStringSubmatch(sc.Text()); m != nil {
			if pct, err := strconv.ParseFloat(m[3], 64); err == nil {
				result[m[2]] = pct
			}
		}
	}
	return result
}

func binarySizeKB(root string) int {
	tmp, _ := os.MkdirTemp("", "driftreport-*")
	defer func() { _ = os.RemoveAll(tmp) }()
	bin := filepath.Join(tmp, "ventd")
	cmd := exec.Command("go", "build", "-ldflags=-s -w", "-o", bin, "./cmd/ventd")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if err := cmd.Run(); err != nil {
		return -1
	}
	info, err := os.Stat(bin)
	if err != nil {
		return -1
	}
	return int(info.Size() / 1024)
}

func depGraphLines(root string) int {
	out, err := exec.Command("go", "mod", "graph").Output()
	if err != nil {
		return -1
	}
	return strings.Count(string(out), "\n")
}

func halImplCount(root string) int {
	halDir := filepath.Join(root, "internal", "hal")
	entries, err := os.ReadDir(halDir)
	if err != nil {
		return -1
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgDir := filepath.Join(halDir, e.Name())
		files, _ := filepath.Glob(filepath.Join(pkgDir, "*.go"))
		methods := make(map[string]bool)
		for _, f := range files {
			data, _ := os.ReadFile(f)
			for _, m := range halMethodRE.FindAllStringSubmatch(string(data), -1) {
				methods[m[1]] = true
			}
		}
		required := []string{"Enumerate", "Read", "Write", "Restore", "Close", "Name"}
		full := true
		for _, r := range required {
			if !methods[r] {
				full = false
				break
			}
		}
		if full {
			count++
		}
	}
	return count
}

func computeDrifts(cur, base Snapshot, thr Thresholds) []string {
	var drifts []string

	if base.DeadCodeCount >= 0 && cur.DeadCodeCount >= 0 {
		if delta := cur.DeadCodeCount - base.DeadCodeCount; delta > thr.DeadCodeIncrease {
			drifts = append(drifts, fmt.Sprintf("dead code symbols increased by %d (threshold %d): %d → %d",
				delta, thr.DeadCodeIncrease, base.DeadCodeCount, cur.DeadCodeCount))
		}
	}
	if base.DupLOC >= 0 && cur.DupLOC >= 0 {
		if delta := cur.DupLOC - base.DupLOC; delta > thr.DupLOCIncrease {
			drifts = append(drifts, fmt.Sprintf("duplicate LOC increased by %d (threshold %d): %d → %d",
				delta, thr.DupLOCIncrease, base.DupLOC, cur.DupLOC))
		}
	}
	for pkg, curPct := range cur.Coverage {
		if basePct, ok := base.Coverage[pkg]; ok {
			if drop := basePct - curPct; drop > thr.CoverageRegressionPct {
				drifts = append(drifts, fmt.Sprintf("coverage regression in %s: %.1f%% → %.1f%% (threshold %.1f%%)",
					pkg, basePct, curPct, thr.CoverageRegressionPct))
			}
		}
	}
	if base.BinarySizeKB > 0 && cur.BinarySizeKB > 0 {
		if delta := cur.BinarySizeKB - base.BinarySizeKB; delta > thr.BinarySizeDeltaKB {
			drifts = append(drifts, fmt.Sprintf("binary size grew %d KB (threshold %d KB): %d KB → %d KB",
				delta, thr.BinarySizeDeltaKB, base.BinarySizeKB, cur.BinarySizeKB))
		}
	}
	if base.DepGraphLines > 0 && cur.DepGraphLines > 0 {
		pctGrowth := float64(cur.DepGraphLines-base.DepGraphLines) / float64(base.DepGraphLines) * 100
		if pctGrowth > thr.DepGraphSizePct {
			drifts = append(drifts, fmt.Sprintf("dep graph grew %.1f%% (threshold %.1f%%): %d → %d lines",
				pctGrowth, thr.DepGraphSizePct, base.DepGraphLines, cur.DepGraphLines))
		}
	}
	if base.HALImplCount > 0 && cur.HALImplCount != base.HALImplCount {
		drifts = append(drifts, fmt.Sprintf("HAL implementation count changed: %d → %d (investigate new/removed backends)",
			base.HALImplCount, cur.HALImplCount))
	}
	return drifts
}

func loadThresholds(path string) Thresholds {
	thr := Thresholds{CoverageRegressionPct: 2, BinarySizeDeltaKB: 100, DupLOCIncrease: 50, DeadCodeIncrease: 5, DepGraphSizePct: 10}
	if data, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(data, &thr)
	}
	return thr
}

func loadBaseline(path string) Snapshot {
	s := Snapshot{DeadCodeCount: -1, DupLOC: -1, BinarySizeKB: -1, DepGraphLines: -1, HALImplCount: -1, Coverage: map[string]float64{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func emit(path string, v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	if path == "" {
		fmt.Println(string(data))
		return
	}
	_ = os.WriteFile(path, data, 0644)
}
