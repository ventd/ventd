package deploy

import (
	"os"
	"strings"
	"testing"
)

// TestVentdSetupUnit_RuntimeDirectoryModeIsRestrictive pins the
// bug-hunt fix (Agent 2 #6): the ventd-setup.service unit MUST
// declare RuntimeDirectoryMode at 0750 or tighter so the broker's
// request file isn't world-readable. The original 0755 declaration
// would have leaked the wizard's request contents (module names,
// package selectors, audit metadata) to every user on the system
// the first time ventd-setup happened to win the create race
// against ventd.service.
//
// Allowed values: 0750 (root + ventd group, matches ventd.service)
// or 0700 (root only, tighter still). Anything looser fails.
func TestVentdSetupUnit_RuntimeDirectoryModeIsRestrictive(t *testing.T) {
	body, err := os.ReadFile("ventd-setup.service")
	if err != nil {
		t.Fatalf("read ventd-setup.service: %v", err)
	}
	var found string
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "RuntimeDirectoryMode=") {
			found = strings.TrimPrefix(trimmed, "RuntimeDirectoryMode=")
			found = strings.TrimSpace(found)
			break
		}
	}
	if found == "" {
		t.Fatal("ventd-setup.service missing RuntimeDirectoryMode= directive")
	}
	switch found {
	case "0700", "0750":
		// OK — tight enough.
	default:
		t.Errorf("RuntimeDirectoryMode = %q, want 0700 or 0750 "+
			"(0755+ leaks the wizard's request file to unprivileged users)",
			found)
	}
}

// TestVentdSetupUnit_TypeOneshot pins the systemd Type so a future
// edit can't accidentally turn the broker into a long-running
// service. The wizard's contract is "start, dispatch one
// operation, exit"; Type=simple would leave the unit hanging
// in the active(running) state and confuse the wizard's poll for
// inactive(dead).
func TestVentdSetupUnit_TypeOneshot(t *testing.T) {
	body, err := os.ReadFile("ventd-setup.service")
	if err != nil {
		t.Fatalf("read ventd-setup.service: %v", err)
	}
	if !strings.Contains(string(body), "Type=oneshot") {
		t.Error("ventd-setup.service missing Type=oneshot — broker MUST be oneshot")
	}
	if strings.Contains(string(body), "Type=simple") || strings.Contains(string(body), "Type=notify") {
		t.Error("ventd-setup.service has a long-running Type — wizard expects oneshot")
	}
}

// TestVentdSetupUnit_UserRoot pins the User= directive at root.
// The entire purpose of the broker is the unconfined privileged
// surface; flipping User= to anything else defeats the
// architecture spec-v0_6_0-split-daemon §"Why this works".
func TestVentdSetupUnit_UserRoot(t *testing.T) {
	body, err := os.ReadFile("ventd-setup.service")
	if err != nil {
		t.Fatalf("read ventd-setup.service: %v", err)
	}
	if !strings.Contains(string(body), "User=root") {
		t.Error("ventd-setup.service missing User=root — broker MUST run as root for the privileged operations")
	}
}
