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
// Virtualised: requires ≥3 of {DMI, systemd-detect-virt --vm, /sys/hypervisor}.
// Containerised: requires ≥2 of {/.dockerenv, /proc/1/cgroup, systemd-detect-virt --container}.
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
