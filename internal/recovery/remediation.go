// remediation.go — failure-class remediation catalogue (#800).
//
// Shared between wizard-recovery (calibration error banner) and
// doctor (runtime issue surface). For each FailureClass,
// RemediationFor returns an ordered list of `Remediation` entries
// the UI renders as cards above the existing action buttons.
// Each card is one of:
//
//   - KindActionPost   — "Apply fix" button POSTs to ActionURL,
//                        consumes a structured response (typically
//                        the install-log-stream shape used by
//                        /api/v1/hwdiag/install-*).
//   - KindModalInstr   — "Show instructions" button opens a modal
//                        with copy-to-clipboard shell commands.
//                        Backend returns mokInstructionsResponse.
//   - KindDocsOnly     — external link only (e.g. "Disable Secure
//                        Boot in firmware"). No automation possible.
//
// The catalogue is content, not behaviour: each entry is what the
// operator needs to know about + a link to the action endpoint.
// Adding a class means adding a switch arm here plus the matching
// classifier rule in classify.go.

package recovery

// RemediationKind discriminates the UI render mode.
type RemediationKind string

const (
	KindActionPost RemediationKind = "action_post"
	KindModalInstr RemediationKind = "modal_instr"
	KindDocsOnly   RemediationKind = "docs_only"
)

// Remediation is one card in the calibration error banner. The UI
// renders Label as the card title, Description as the body, and the
// button text + behaviour from Kind + ActionURL.
//
// Keep field names stable — the JSON shape is the operator-facing
// contract for /api/v1/setup/status's new `remediation` array.
type Remediation struct {
	Label       string          `json:"label"`
	Description string          `json:"description,omitempty"`
	Kind        RemediationKind `json:"kind"`
	// ActionURL is the endpoint the UI POSTs to when the
	// operator clicks the card's primary button. Empty for
	// KindDocsOnly entries.
	ActionURL string `json:"action_url,omitempty"`
	// DocURL is an optional secondary link rendered as
	// "Learn more" beside the action button.
	DocURL string `json:"doc_url,omitempty"`
	// RequiresReboot signals that a successful action only takes effect
	// after a reboot — canonical case is ClassSecureBoot's mok-enroll
	// (firmware MOK Manager only runs at boot) and ClassInTreeConflict's
	// reset-and-reinstall (the blacklist drop-in needs a fresh boot to
	// guarantee the in-tree module isn't auto-loaded by another path).
	// The web UI surfaces a "Reboot now / Later" prompt below the card
	// after a successful POST when this is true (#818).
	RequiresReboot bool `json:"requires_reboot,omitempty"`
}

// RemediationFor returns the catalogue entries for class. The slice
// is read-only — callers must not mutate the returned values; a
// future version may use atomic.Pointer for hot-reloadable docs URLs.
//
// Order is deliberate: the most-recommended action is first.
//
// All entries close with a generic "Send diagnostic bundle" option
// so the operator always has a way to escalate to the maintainers.
// That bundle button reuses the existing /api/diag/bundle endpoint
// shipped by PR #799, so no new backend work needed for that arm.
//
// Note on install-time vs runtime classes: `ventd preflight`
// (v0.5.11) catches install-time blockers BEFORE the systemd unit
// runs, so most operators won't see ClassSecureBoot /
// ClassMissingHeaders / etc. as wizard errors. But they CAN still
// fire during wizard re-entry after a failed first attempt — DKMS
// state from the first attempt, in-tree conflicts that were
// auto-fixed but reverted on reboot, etc. — so the auto-fix cards
// stay available here. The narrowing tried in early v0.5.11 was
// too aggressive and removed the auto-fix path operators relied
// on for this re-entry case (caught on Phoenix's HIL).
func RemediationFor(class FailureClass) []Remediation {
	bundle := Remediation{
		Label:       "Send diagnostic bundle to maintainers",
		Description: "Generates a redacted bundle (hostnames, IPs, MACs replaced with stable tokens) you can share with the project maintainers for help.",
		Kind:        KindActionPost,
		ActionURL:   "/api/diag/bundle",
	}

	switch class {
	case ClassSecureBoot:
		return []Remediation{
			{
				Label:       "Generate MOK signing key",
				Description: "Secure Boot blocks unsigned kernel modules. Generate a Machine Owner Key, enroll it at next boot, and ventd will sign its module. Walk-through provided.",
				Kind:        KindModalInstr,
				ActionURL:   "/api/hwdiag/mok-enroll",
				DocURL:      "https://github.com/ventd/ventd/wiki/secure-boot",
				// Firmware MOK Manager runs only at boot — operator must
				// reboot to confirm the enrollment and the queued password.
				RequiresReboot: true,
			},
			{
				Label:       "Or disable Secure Boot in firmware",
				Description: "Reboot to BIOS/UEFI setup, find Secure Boot under the Boot or Security menu, set it to Disabled. Faster but less secure than enrolling a MOK.",
				Kind:        KindDocsOnly,
				DocURL:      "https://github.com/ventd/ventd/wiki/secure-boot#disable-in-firmware",
			},
			bundle,
		}

	case ClassMissingHeaders:
		return []Remediation{
			{
				Label:       "Install kernel headers",
				Description: "Installs linux-headers (Debian/Ubuntu), kernel-headers (Fedora), or linux-headers (Arch) for your running kernel. The OOT driver build will succeed once these are present.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/install-kernel-headers",
				DocURL:      "https://github.com/ventd/ventd/wiki/kernel-headers",
			},
			bundle,
		}

	case ClassDKMSBuildFailed:
		return []Remediation{
			{
				Label:       "Install DKMS",
				Description: "DKMS rebuilds out-of-tree drivers automatically when the kernel updates. If it isn't installed, the wizard's driver-install step fails before the build even starts.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/install-dkms",
				DocURL:      "https://github.com/ventd/ventd/wiki/dkms",
			},
			bundle,
		}

	case ClassMissingBuildTools:
		return []Remediation{
			{
				Label:       "Install build tools",
				Description: "Installs gcc, make, and the distro's build-essentials meta-package. The OOT driver build needs these to compile against your kernel.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/install-build-tools",
				DocURL:      "https://github.com/ventd/ventd/wiki/build-tools",
			},
			bundle,
		}

	case ClassDKMSStateCollision:
		return []Remediation{
			{
				Label:       "Reset and reinstall driver",
				Description: "Removes any partially-installed driver state (DKMS registration, .ko files, modules-load.d entries) and runs a fresh install. Use this when a previous install attempt left half-finished state behind.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/reset-and-reinstall",
				DocURL:      "https://github.com/ventd/ventd/wiki/reset-and-reinstall",
			},
			bundle,
		}

	case ClassInTreeConflict:
		return []Remediation{
			{
				Label:       "Unbind in-tree driver and blacklist",
				Description: "Removes the conflicting in-tree kernel driver (e.g. nct6683 when ventd needs nct6687d) and writes a blacklist drop-in so it doesn't reload at boot. Then reruns the OOT install.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/reset-and-reinstall",
				DocURL:      "https://github.com/ventd/ventd/wiki/in-tree-conflict",
				// Reboot guarantees the blacklist takes full effect — modprobe
				// -r clears the running module, but a stray udev rule or
				// initramfs hook can still reload it before ventd's modprobe
				// fires. A reboot tests the boot-time blacklist before the
				// operator hits the same wall again on next power-on.
				RequiresReboot: true,
			},
			bundle,
		}

	case ClassContainerised:
		return []Remediation{
			{
				Label:       "Run ventd on the host instead",
				Description: "ventd cannot calibrate fans from inside a container — hwmon writes don't reach real hardware. Install and run ventd directly on the host system.",
				Kind:        KindDocsOnly,
				DocURL:      "https://github.com/ventd/ventd/wiki/containers",
			},
			bundle,
		}

	case ClassPackageManagerBusy:
		return []Remediation{
			{
				Label:       "Wait and retry",
				Description: "Another package manager (apt/dpkg) is currently running. Wait for it to finish (or close any open Software Updater windows), then retry the install.",
				Kind:        KindDocsOnly,
				DocURL:      "https://github.com/ventd/ventd/wiki/apt-lock",
			},
			bundle,
		}

	case ClassDaemonNotRoot:
		return []Remediation{
			{
				Label:       "Configure passwordless sudo or run as root",
				Description: "ventd is not running as root and cannot escalate non-interactively. Either run the daemon as root (via systemd unit) or configure passwordless sudo for the ventd user.",
				Kind:        KindDocsOnly,
				DocURL:      "https://github.com/ventd/ventd/wiki/sudo",
			},
			bundle,
		}

	case ClassReadOnlyRootfs:
		return []Remediation{
			{
				Label:       "Use your distro's system-modification path",
				Description: "/lib/modules is read-only on this distro (Silverblue, NixOS, Ubuntu Core, etc.). Driver install requires using the distro's system-modification procedure — see docs.",
				Kind:        KindDocsOnly,
				DocURL:      "https://github.com/ventd/ventd/wiki/immutable-distros",
			},
			bundle,
		}

	case ClassDiskFull:
		return []Remediation{
			{
				Label:       "Free disk space",
				Description: "One of /lib/modules, /usr/src, or /var/cache has less than 256 MiB free. Free up space — typically by clearing the package cache or old kernel headers — then retry.",
				Kind:        KindDocsOnly,
				DocURL:      "https://github.com/ventd/ventd/wiki/disk-full",
			},
			bundle,
		}

	case ClassConcurrentInstall:
		return []Remediation{
			{
				Label:       "Wait or take over the running wizard",
				Description: "Another ventd setup wizard is already running on this machine. Wait for it to finish, or take over the run (the existing wizard's state will be released).",
				Kind:        KindActionPost,
				ActionURL:   "/api/setup/take-over",
				DocURL:      "https://github.com/ventd/ventd/wiki/concurrent-wizard",
			},
			bundle,
		}

	case ClassACPIResourceConflict:
		return []Remediation{
			{
				Label:       "Add acpi_enforce_resources=lax to kernel parameters",
				Description: "Your motherboard's BIOS reserves the SuperIO chip's I/O region via ACPI, blocking ventd's driver from binding. This kernel parameter relaxes that claim. ventd will write a GRUB drop-in, run update-grub, and prompt for reboot. After reboot, the driver will bind and PWM channels will appear.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/grub-cmdline-add",
				DocURL:      "https://github.com/ventd/ventd/wiki/acpi-resource-conflict",
			},
			bundle,
		}

	case ClassDriverWontBind:
		// Trio of real actions when the driver installed but
		// won't bind. Reset+reinstall covers stale-state cases;
		// ACPI workaround covers the ~70% of bind failures on
		// MSI/ASUS Z690-class boards. Bundle is the escape hatch.
		// Operators get usable choices instead of bundle-only.
		return []Remediation{
			{
				Label:       "Reset and reinstall driver",
				Description: "Clears DKMS state, removes the .ko file, and runs a fresh install. Use this first — the most common cause of bind failure on retry is stale state from a prior attempt.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/reset-and-reinstall",
				DocURL:      "https://github.com/ventd/ventd/wiki/reset-and-reinstall",
			},
			{
				Label:       "Try the ACPI workaround (acpi_enforce_resources=lax)",
				Description: "Many MSI / ASUS / Gigabyte Z690-class motherboards reserve the SuperIO chip's I/O region via ACPI, blocking the driver from binding. This kernel parameter relaxes that claim. ventd will write a GRUB drop-in and prompt for reboot.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/grub-cmdline-add",
				DocURL:      "https://github.com/ventd/ventd/wiki/acpi-resource-conflict",
			},
			bundle,
		}

	case ClassApparmorDenied:
		return []Remediation{
			{
				Label:       "Reload AppArmor profile",
				Description: "Loads ventd's shipped AppArmor profile into the running kernel. Distros that enforce AppArmor at boot may not have parsed our profile yet — this wires it up so the wizard's helpers run unblocked.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/load-apparmor",
				DocURL:      "https://github.com/ventd/ventd/wiki/apparmor",
			},
			bundle,
		}

	case ClassMissingModule:
		// Runtime class: a module that disappeared after install
		// (manual rmmod, kernel update without DKMS rebuild) reaches
		// the doctor surface, not the wizard. Keep the load-module
		// card for that path.
		return []Remediation{
			{
				Label:       "Try loading the module",
				Description: "Asks the daemon to modprobe the expected kernel module and persist it via /etc/modules-load.d. If the module isn't installed at all, this surfaces a more specific error.",
				Kind:        KindActionPost,
				ActionURL:   "/api/setup/load-module",
				DocURL:      "https://github.com/ventd/ventd/wiki/missing-module",
			},
			bundle,
		}

	case ClassVendorDaemonActive:
		// Detect-and-defer to vendor-shipped fan daemons (R28 Agent G's
		// #1 architectural finding). The right move is monitor-only —
		// the vendor daemon is already controlling fans correctly on
		// these Linux-first OEM laptops. Installing ventd on top creates
		// conflict, not value.
		return []Remediation{
			{
				Label:       "Switch ventd to monitor-only mode",
				Description: "Your laptop's vendor daemon (system76-power, asusctl, tccd, slimbookbattery, …) already controls fans correctly. ventd will register temperature and PWM readings as monitor-only and step out of the vendor's way. You can re-enable control later from Settings if you uninstall the vendor tool.",
				Kind:        KindActionPost,
				ActionURL:   "/api/setup/apply-monitor-only",
				DocURL:      "https://github.com/ventd/ventd/wiki/vendor-daemon-coexistence",
			},
			bundle,
		}

	case ClassThinkpadACPIDisabled:
		return []Remediation{
			{
				Label:       "Enable thinkpad_acpi fan_control",
				Description: "ThinkPads ship with the kernel's `thinkpad_acpi` driver but its fan_control parameter is off by default — Lenovo's docs warn that the EC may override software writes. ventd will write /etc/modprobe.d/ventd-thinkpad.conf with `options thinkpad_acpi fan_control=1` and reload the module. Reboot recommended so the EC re-arbitrates with the option flipped.",
				Kind:        KindActionPost,
				ActionURL:   "/api/hwdiag/modprobe-options-write",
				DocURL:      "https://github.com/ventd/ventd/wiki/thinkpad-fan-control",
				// Reboot prompt picks up via #828 once that lands; for
				// now the wiki page covers the manual reboot step.
			},
			bundle,
		}

	case ClassNixOSPathIgnored:
		return []Remediation{
			{
				Label:       "NixOS: declarative integration required",
				Description: "ventd writes modprobe drop-ins to /etc/modprobe.d/, but NixOS silently ignores those paths in favour of declarative configuration.nix entries. The fix isn't a click here — see the wiki for the exact `boot.extraModprobeConfig` and `boot.extraModulePackages` expressions to add to your configuration.nix, then run `nixos-rebuild switch`.",
				Kind:        KindDocsOnly,
				DocURL:      "https://github.com/ventd/ventd/wiki/nixos",
			},
			bundle,
		}

	default:
		// ClassUnknown — generic bundle option only.
		return []Remediation{bundle}
	}
}
