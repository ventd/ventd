package corsair

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUdevRule_Parses(t *testing.T) {
	// Locate deploy/90-ventd-liquid.rules relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	// Navigate from internal/hal/liquid/corsair/ to the repo root, then to deploy/
	testDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(testDir, "..", "..", "..", "..")
	ruleFile := filepath.Join(repoRoot, "deploy", "90-ventd-liquid.rules")

	content, err := os.ReadFile(ruleFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", ruleFile, err)
	}

	if len(content) == 0 {
		t.Fatal("90-ventd-liquid.rules is empty")
	}

	// Check for required strings.
	text := string(content)
	if !strings.Contains(text, `ATTRS{idVendor}=="1b1c"`) {
		t.Error("rule does not contain ATTRS{idVendor}==\"1b1c\"")
	}
	if !strings.Contains(text, `TAG+="uaccess"`) {
		t.Error("rule does not contain TAG+=\"uaccess\"")
	}

	// Count non-comment, non-blank lines.
	lines := bytes.Split(content, []byte("\n"))
	activeLineCount := 0
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 && !bytes.HasPrefix(trimmed, []byte("#")) {
			activeLineCount++
		}
	}

	if activeLineCount < 1 || activeLineCount > 3 {
		t.Errorf("expected 1–3 non-comment, non-blank lines, got %d", activeLineCount)
	}
}
