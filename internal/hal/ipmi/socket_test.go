package ipmi_test

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// parseUnit parses a systemd unit file into a map of key → list of values.
// Section headers, blank lines, and # comments are skipped.
// Multi-value keys (e.g. DeviceAllow) accumulate every occurrence.
func parseUnit(data string) map[string][]string {
	out := make(map[string][]string)
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '[' {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		out[k] = append(out[k], v)
	}
	return out
}

func loadUnit(t *testing.T, name string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile("../../../deploy/" + name)
	if err != nil {
		t.Fatalf("read deploy/%s: %v", name, err)
	}
	return parseUnit(string(data))
}

// TestMainUnit_NoIpmiDeviceGrant verifies that deploy/ventd.service grants no
// IPMI device access and retains no capabilities. This is the configuration
// invariant for the privilege-separation boundary introduced in PR 2.
func TestMainUnit_NoIpmiDeviceGrant(t *testing.T) {
	u := loadUnit(t, "ventd.service")

	t.Run("no_ipmi_device_allow", func(t *testing.T) {
		for _, v := range u["DeviceAllow"] {
			if strings.Contains(v, "/dev/ipmi") {
				t.Errorf("ventd.service DeviceAllow grants IPMI access: %q", v)
			}
		}
	})

	t.Run("capability_bounding_set_empty", func(t *testing.T) {
		vals := u["CapabilityBoundingSet"]
		if len(vals) != 1 || vals[0] != "" {
			t.Errorf("CapabilityBoundingSet: want exactly empty, got %v", vals)
		}
	})

	t.Run("no_ambient_cap_sys_rawio", func(t *testing.T) {
		for _, v := range u["AmbientCapabilities"] {
			if strings.Contains(v, "CAP_SYS_RAWIO") {
				t.Errorf("ventd.service AmbientCapabilities grants CAP_SYS_RAWIO: %q", v)
			}
		}
	})
}

// assertHasValue fails unless key has at least one value equal to want.
func assertHasValue(t *testing.T, u map[string][]string, key, want string) {
	t.Helper()
	if !slices.Contains(u[key], want) {
		t.Errorf("%s: want %q in %v", key, want, u[key])
	}
}

// TestSidecarUnit_MinimalPrivilege verifies that deploy/ventd-ipmi.service
// applies the minimal-privilege profile from .claude/rules/ipmi-safety.md:
// exactly one device grant (/dev/ipmi0 rw), a single capability
// (CAP_SYS_RAWIO), and the full filesystem+network hardening set.
func TestSidecarUnit_MinimalPrivilege(t *testing.T) {
	u := loadUnit(t, "ventd-ipmi.service")

	t.Run("ipmi0_device_allow_rw", func(t *testing.T) {
		found := false
		for _, v := range u["DeviceAllow"] {
			if strings.Contains(v, "/dev/ipmi0") && strings.Contains(v, "rw") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DeviceAllow=/dev/ipmi0 rw not found; got %v", u["DeviceAllow"])
		}
	})

	t.Run("capability_bounding_set_rawio_only", func(t *testing.T) {
		vals := u["CapabilityBoundingSet"]
		if len(vals) != 1 || vals[0] != "CAP_SYS_RAWIO" {
			t.Errorf("CapabilityBoundingSet: want exactly [CAP_SYS_RAWIO], got %v", vals)
		}
	})

	t.Run("ambient_capabilities_rawio", func(t *testing.T) {
		assertHasValue(t, u, "AmbientCapabilities", "CAP_SYS_RAWIO")
	})

	t.Run("no_new_privileges", func(t *testing.T) {
		assertHasValue(t, u, "NoNewPrivileges", "yes")
	})

	t.Run("protect_system_strict", func(t *testing.T) {
		assertHasValue(t, u, "ProtectSystem", "strict")
	})

	t.Run("user_ventd_ipmi", func(t *testing.T) {
		assertHasValue(t, u, "User", "ventd-ipmi")
	})

	t.Run("restrict_address_families_unix_only", func(t *testing.T) {
		assertHasValue(t, u, "RestrictAddressFamilies", "AF_UNIX")
	})

	t.Run("type_notify", func(t *testing.T) {
		assertHasValue(t, u, "Type", "notify")
	})

	t.Run("syscall_architectures_native", func(t *testing.T) {
		assertHasValue(t, u, "SystemCallArchitectures", "native")
	})
}
