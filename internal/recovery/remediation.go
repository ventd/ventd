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
// v0.5.11 narrowing: install-time classes (Secure Boot, missing
// kernel headers, missing build tools, DKMS state collisions, in-
// tree conflicts, container/rootfs/sudo refusal, disk full,
// concurrent install, package-manager busy) used to render auto-fix
// cards in the calibration error banner. Now they are caught by
// `ventd preflight` BEFORE the systemd unit is even installed —
// the operator can't reach the calibration UI with these
// blockers active. The classes stay in the enum for diag-bundle
// context (a calibration-time error referencing one of these still
// helps maintainers triage), but their UI cards collapse to just
// the bundle option. Runtime classes that can fire after install
// (AppArmor profile drift after a kernel update, missing module
// after manual rmmod) keep their cards because they reach the
// doctor surface, not the wizard.
func RemediationFor(class FailureClass) []Remediation {
	bundle := Remediation{
		Label:       "Send diagnostic bundle to maintainers",
		Description: "Generates a redacted bundle (hostnames, IPs, MACs replaced with stable tokens) you can share with the project maintainers for help.",
		Kind:        KindActionPost,
		ActionURL:   "/api/diag/bundle",
	}

	// Install-time classes — the operator cannot reach this surface
	// while one of these is active because `ventd preflight` blocks
	// install. Returned remediation is bundle-only so the doctor
	// view stays consistent if a runtime regression resurfaces one.
	installTime := map[FailureClass]bool{
		ClassSecureBoot:         true,
		ClassMissingHeaders:     true,
		ClassDKMSBuildFailed:    true,
		ClassMissingBuildTools:  true,
		ClassDKMSStateCollision: true,
		ClassInTreeConflict:     true,
		ClassContainerised:      true,
		ClassPackageManagerBusy: true,
		ClassDaemonNotRoot:      true,
		ClassReadOnlyRootfs:     true,
		ClassDiskFull:           true,
		ClassConcurrentInstall:  true,
	}
	if installTime[class] {
		return []Remediation{bundle}
	}

	switch class {
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

	default:
		// ClassUnknown — generic bundle option only.
		return []Remediation{bundle}
	}
}
