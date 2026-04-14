package hwmon

import (
	"os"
	"strings"
)

// DistroInfo identifies the running distribution just enough to produce
// correct user-facing command strings in the MOK-enrollment instructions
// panel. ID follows os-release semantics (e.g. "debian", "ubuntu", "fedora",
// "arch", "opensuse-tumbleweed", "alpine").
type DistroInfo struct {
	ID       string
	IDLike   string
	PrettyID string
}

// DetectDistro parses /etc/os-release. Empty fields on any error — callers
// fall through to generic instructions.
func DetectDistro() DistroInfo {
	return parseOSRelease(readOrEmpty("/etc/os-release"))
}

func readOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func parseOSRelease(content string) DistroInfo {
	var d DistroInfo
	for _, line := range strings.Split(content, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.TrimSpace(k) {
		case "ID":
			d.ID = v
		case "ID_LIKE":
			d.IDLike = v
		case "PRETTY_NAME":
			d.PrettyID = v
		}
	}
	return d
}

// MOKInstallCommand returns the distro-appropriate package-install command
// for `mokutil`, used verbatim in the MOK-enrollment instructions panel.
func (d DistroInfo) MOKInstallCommand() string {
	switch d.familyKey() {
	case "debian":
		return "sudo apt-get install -y mokutil"
	case "fedora":
		return "sudo dnf install -y mokutil"
	case "arch":
		return "sudo pacman -S --noconfirm mokutil"
	case "suse":
		return "sudo zypper install -y mokutil"
	case "alpine":
		return "sudo apk add mokutil"
	}
	return "install the `mokutil` package for your distribution"
}

// familyKey collapses ID and ID_LIKE into a broad family bucket so callers
// don't have to enumerate every derivative (Linux Mint, Manjaro, etc.).
func (d DistroInfo) familyKey() string {
	ids := strings.ToLower(d.ID + " " + d.IDLike)
	switch {
	case strings.Contains(ids, "debian"), strings.Contains(ids, "ubuntu"):
		return "debian"
	case strings.Contains(ids, "fedora"), strings.Contains(ids, "rhel"), strings.Contains(ids, "centos"):
		return "fedora"
	case strings.Contains(ids, "arch"), strings.Contains(ids, "manjaro"):
		return "arch"
	case strings.Contains(ids, "suse"):
		return "suse"
	case strings.Contains(ids, "alpine"):
		return "alpine"
	}
	return ""
}
