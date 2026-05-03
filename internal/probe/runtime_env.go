package probe

import (
	"context"
	"io/fs"
	"strings"
)

// virtVendors maps lowercase DMI sys_vendor / product_name substrings to
// the canonical virt type string. Checked against both vendor and product.
var virtVendors = map[string]string{
	"kvm":                   "kvm",
	"qemu":                  "qemu",
	"vmware":                "vmware",
	"microsoft corporation": "hyperv",
	"innotek gmbh":          "virtualbox",
	"xen":                   "xen",
	"parallels":             "parallels",
}

// detectEnvironment checks for virtualisation (§4.1 RULE-PROBE-02) and
// containerisation (§4.1 RULE-PROBE-03). Returns the populated RuntimeEnvironment
// and any diagnostic events.
//
// Virtualised: requires ≥3 of {DMI, systemd-detect-virt --vm, /sys/hypervisor,
//   cpuinfo "hypervisor" flag}. The cpuinfo source was added 2026-05-03 to close
//   the MicroVM/Firecracker recall gap (those hosts can fire the cpuid
//   hypervisor bit alone with no DMI / sysfs / systemd evidence).
// Containerised: requires ≥2 of {/.dockerenv, /proc/1/cgroup, systemd-detect-virt
//   --container, /proc/mounts overlay-root, /run/.containerenv, /proc/1/environ
//   container=}.
func (p *prober) detectEnvironment(ctx context.Context) (RuntimeEnvironment, []Diagnostic) {
	var env RuntimeEnvironment
	var diags []Diagnostic

	// --- Container detection (three sources, ≥2 required) ---
	containerScore := 0
	var containerRuntime, containerSignals []string

	// Source 1: /.dockerenv
	if p.cfg.RootFS != nil {
		if _, err := fs.Stat(p.cfg.RootFS, ".dockerenv"); err == nil {
			containerScore++
			containerSignals = append(containerSignals, "/.dockerenv")
			if containerRuntime == nil {
				containerRuntime = append(containerRuntime, "docker")
			}
		}
	}

	// Source 2: /proc/1/cgroup
	if p.cfg.ProcFS != nil {
		if data, err := fs.ReadFile(p.cfg.ProcFS, "1/cgroup"); err == nil {
			s := strings.ToLower(string(data))
			switch {
			case strings.Contains(s, "docker"):
				containerScore++
				containerSignals = append(containerSignals, "/proc/1/cgroup:docker")
				containerRuntime = append(containerRuntime, "docker")
			case strings.Contains(s, "lxc"):
				containerScore++
				containerSignals = append(containerSignals, "/proc/1/cgroup:lxc")
				containerRuntime = append(containerRuntime, "lxc")
			case strings.Contains(s, "kubepods"):
				containerScore++
				containerSignals = append(containerSignals, "/proc/1/cgroup:kubepods")
				containerRuntime = append(containerRuntime, "kubepods")
			case strings.Contains(s, "garden"):
				containerScore++
				containerSignals = append(containerSignals, "/proc/1/cgroup:garden")
				containerRuntime = append(containerRuntime, "garden")
			}
		}
	}

	// Source 3: systemd-detect-virt --container
	if out, err := p.cfg.ExecFn(ctx, "systemd-detect-virt", "--container"); err == nil && out != "none" && out != "" {
		containerScore++
		containerSignals = append(containerSignals, "systemd-detect-virt:"+out)
		containerRuntime = append(containerRuntime, out)
	}

	// Source 4: overlay root filesystem in /proc/mounts (Docker on cgroup v2 hosts).
	// On cgroup v2 (Ubuntu 22.04+, Debian 12+) /proc/1/cgroup shows only "0::/" with
	// no container keywords, so this signal catches Docker containers that Source 2
	// would miss. Bare-metal systems never use overlay as their root filesystem.
	if p.cfg.ProcFS != nil {
		if data, err := fs.ReadFile(p.cfg.ProcFS, "mounts"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 3 && fields[1] == "/" && fields[2] == "overlay" {
					containerScore++
					containerSignals = append(containerSignals, "/proc/mounts:overlay-root")
					if len(containerRuntime) == 0 {
						containerRuntime = append(containerRuntime, "docker")
					}
					break
				}
			}
		}
	}

	// Source 5: /run/.containerenv (Podman / Toolbx / Distrobox canonical
	// marker). Research-validated: Podman rootless without this signal
	// fires only one of the prior four sources (systemd-detect-virt) and
	// falls below the score threshold — so a podman-rootless install
	// would silently proceed with bogus hwmon writes. Adding this source
	// brings podman from 1-of-4 to 2-of-5, correctly flagged.
	if p.cfg.RootFS != nil {
		if _, err := fs.Stat(p.cfg.RootFS, "run/.containerenv"); err == nil {
			containerScore++
			containerSignals = append(containerSignals, "/run/.containerenv")
			if len(containerRuntime) == 0 {
				containerRuntime = append(containerRuntime, "podman")
			}
		}
	}

	// Source 6: container= in /proc/1/environ (systemd-nspawn canonical;
	// also podman / toolbx / distrobox). Research-validated: nspawn
	// publishes ONLY this signal — no /.dockerenv, no overlay root, no
	// docker keyword in cgroup. Pre-fix, nspawn fell through to "bare
	// metal" detection. Adds the second authoritative source.
	if p.cfg.ProcFS != nil {
		if data, err := fs.ReadFile(p.cfg.ProcFS, "1/environ"); err == nil {
			// /proc/1/environ is NUL-separated. Look for the
			// `container=<name>` token.
			for _, kv := range strings.Split(string(data), "\x00") {
				if v, ok := strings.CutPrefix(kv, "container="); ok && v != "" {
					containerScore++
					containerSignals = append(containerSignals, "/proc/1/environ:container="+v)
					if len(containerRuntime) == 0 {
						containerRuntime = append(containerRuntime, v)
					}
					break
				}
			}
		}
	}

	if containerScore >= 2 {
		env.Containerised = true
		if len(containerRuntime) > 0 {
			env.ContainerRuntime = containerRuntime[0]
		}
		env.DetectedVia = containerSignals
		diags = append(diags, Diagnostic{
			Severity: "info",
			Code:     "PROBE-CONTAINER-DETECTED",
			Message:  "containerised environment detected; refusing install",
			Context:  map[string]string{"signals": strings.Join(containerSignals, ","), "runtime": env.ContainerRuntime},
		})
		return env, diags
	} else if containerScore == 1 {
		diags = append(diags, Diagnostic{
			Severity: "info",
			Code:     "PROBE-CONTAINER-SINGLE-SOURCE",
			Message:  "single-source container signal recorded; not sufficient to refuse",
			Context:  map[string]string{"signals": strings.Join(containerSignals, ",")},
		})
	}

	// --- Virtualisation detection (three sources, ≥3 required) ---
	virtScore := 0
	var virtType string
	var virtSignals []string

	// Source 1: DMI sys_vendor / product_name
	if p.cfg.SysFS != nil {
		vendor, _ := readTrimmed(p.cfg.SysFS, "class/dmi/id/sys_vendor")
		product, _ := readTrimmed(p.cfg.SysFS, "class/dmi/id/product_name")
		lv := strings.ToLower(vendor)
		lp := strings.ToLower(product)
		for sub, vtype := range virtVendors {
			if strings.Contains(lv, sub) || strings.Contains(lp, sub) {
				virtScore++
				virtSignals = append(virtSignals, "dmi:"+vtype)
				if virtType == "" {
					virtType = vtype
				}
				break
			}
		}
	}

	// Source 2: systemd-detect-virt --vm
	if out, err := p.cfg.ExecFn(ctx, "systemd-detect-virt", "--vm"); err == nil && out != "none" && out != "" {
		virtScore++
		virtSignals = append(virtSignals, "systemd-detect-virt:"+out)
		if virtType == "" {
			virtType = out
		}
	}

	// Source 3: /sys/hypervisor existence
	if p.cfg.SysFS != nil {
		if _, err := fs.Stat(p.cfg.SysFS, "hypervisor"); err == nil {
			virtScore++
			virtSignals = append(virtSignals, "/sys/hypervisor")
		}
	}

	// Source 4: /proc/cpuinfo "hypervisor" flag. MicroVMs (Firecracker,
	// Cloud Hypervisor) and pre-systemd VMs may set the cpuid hypervisor
	// bit but lack DMI strings, /sys/hypervisor, and systemd-detect-virt
	// — without this 4th source ventd misses the virt detection threshold
	// (≥3) on those hosts and runs the install path on a virtualised
	// machine. Per RULE-PROBE-02 the threshold stays at 3 to keep recall
	// conservative; this signal closes the recall gap without lowering
	// the bar.
	if p.cfg.ProcFS != nil {
		if data, err := fs.ReadFile(p.cfg.ProcFS, "cpuinfo"); err == nil {
			s := string(data)
			// `flags` line lists CPU feature flags space-separated; the
			// kernel guarantees `hypervisor` is space-delimited so a
			// substring match is enough.
			if strings.Contains(s, " hypervisor ") || strings.HasSuffix(s, " hypervisor") {
				virtScore++
				virtSignals = append(virtSignals, "cpuinfo:hypervisor")
			}
		}
	}

	if virtScore >= 3 {
		env.Virtualised = true
		env.VirtType = virtType
		env.DetectedVia = virtSignals
		diags = append(diags, Diagnostic{
			Severity: "info",
			Code:     "PROBE-VIRT-DETECTED",
			Message:  "virtualised environment detected; refusing install",
			Context:  map[string]string{"signals": strings.Join(virtSignals, ","), "type": virtType},
		})
	} else if virtScore > 0 {
		diags = append(diags, Diagnostic{
			Severity: "info",
			Code:     "PROBE-VIRT-SINGLE-SOURCE",
			Message:  "partial virtualisation signal recorded; not sufficient to refuse",
			Context:  map[string]string{"signals": strings.Join(virtSignals, ","), "score": itoa(virtScore)},
		})
	}

	return env, diags
}

// readTrimmed reads a file from the given FS and returns trimmed string content.
func readTrimmed(fsys fs.FS, path string) (string, error) {
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func itoa(n int) string {
	switch n {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	default:
		return "many"
	}
}
