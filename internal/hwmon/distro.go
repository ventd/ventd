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
	return d.installCmd("mokutil")
}

// KmodInstallCommand returns the distro-appropriate package-install command
// for the package that ships `sign-file`. On Debian/Fedora/SUSE/Alpine the
// package is `kmod`; on Arch the helper is bundled with `linux-headers` so
// the install command points there instead. Used by the SecureBoot
// missing-sign-file remediation card.
func (d DistroInfo) KmodInstallCommand() string {
	if d.familyKey() == "arch" {
		// pacman ships sign-file alongside linux-headers; no separate package.
		return "sudo pacman -S --noconfirm linux-headers"
	}
	return d.installCmd("kmod")
}

// BuildToolsInstallCommand returns the distro-appropriate command to install
// gcc + make + the umbrella build-essentials meta-package. Used by the
// missing-build-tools remediation card.
func (d DistroInfo) BuildToolsInstallCommand() string {
	switch d.familyKey() {
	case "debian":
		return "sudo apt-get install -y build-essential"
	case "fedora":
		return "sudo dnf install -y gcc make"
	case "arch":
		return "sudo pacman -S --noconfirm base-devel"
	case "suse":
		return "sudo zypper install -y gcc make"
	case "alpine":
		return "sudo apk add build-base"
	}
	return "install gcc and make for your distribution"
}

// BlacklistDropInPath returns the path of the modprobe drop-in this distro
// loads at boot to blacklist a kernel module. Every glibc-distro plus
// Alpine reads /etc/modprobe.d/*.conf, so the path is identical across
// the families this binary supports — kept on DistroInfo so future
// distro-specific divergence is a one-place edit.
func (d DistroInfo) BlacklistDropInPath() string {
	return "/etc/modprobe.d/ventd-blacklist.conf"
}

// installCmd is the small dispatch helper shared by MOKInstallCommand,
// KmodInstallCommand, and any future single-package installer. Returns
// generic guidance when the family is unknown.
func (d DistroInfo) installCmd(pkg string) string {
	switch d.familyKey() {
	case "debian":
		return "sudo apt-get install -y " + pkg
	case "fedora":
		return "sudo dnf install -y " + pkg
	case "arch":
		return "sudo pacman -S --noconfirm " + pkg
	case "suse":
		return "sudo zypper install -y " + pkg
	case "alpine":
		return "sudo apk add " + pkg
	}
	return "install the `" + pkg + "` package for your distribution"
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
