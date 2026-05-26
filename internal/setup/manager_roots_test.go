package setup

import (
	"io"
	"log/slog"
	"testing"
)

func TestNew_UsesProductionRoots(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(nil, logger)
	if m.hwmonRoot != defaultHwmonRoot {
		t.Errorf("hwmonRoot = %q, want %q", m.hwmonRoot, defaultHwmonRoot)
	}
	if m.procRoot != defaultProcRoot {
		t.Errorf("procRoot = %q, want %q", m.procRoot, defaultProcRoot)
	}
	if m.powercapRoot != defaultPowercapRoot {
		t.Errorf("powercapRoot = %q, want %q", m.powercapRoot, defaultPowercapRoot)
	}
}
