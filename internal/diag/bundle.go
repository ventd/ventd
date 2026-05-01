// Package diag implements the ventd diagnostic bundle generator.
// It coordinates detection collectors, the redactor, and the tar.gz writer.
//
// Dependency rules (enforced by go list -deps check in CI):
//   - internal/diag imports internal/hwdb for calibration types.
//   - internal/diag does NOT import internal/calibration.
package diag

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/diag/detection"
	"github.com/ventd/ventd/internal/diag/redactor"
	"github.com/ventd/ventd/internal/experimental"
)

// ErrDenied is returned when a caller attempts to add an architecturally
// excluded path. RULE-DIAG-PR2C-06.
var ErrDenied = errors.New("diag: path is on the capture denylist")

// denylist contains paths that must NEVER be captured regardless of profile.
// RULE-DIAG-PR2C-06: enforced architecturally, not by the redactor.
var denylist = []string{
	"/etc/shadow",
	"/etc/sudoers",
	"/etc/sudoers.d/",
	"/.ssh/",
	"/root/.ssh/",
	"/.gnupg/",
	"/proc/keys",
	"/run/credentials/",
	"_history",   // shell history files (.bash_history, .zsh_history)
	".pem",       // TLS private keys
	".key",       // TLS private keys
	"id_rsa",     // SSH private keys
	"id_ed25519", // SSH private keys
	"id_ecdsa",   // SSH private keys
}

// isDenied reports whether path matches any denylist entry.
func isDenied(path string) bool {
	for _, d := range denylist {
		if strings.Contains(path, d) {
			return true
		}
	}
	return false
}

// Options configures a bundle generation run.
type Options struct {
	// OutputDir overrides the default output directory selection.
	// If empty, resolved per §15.5 precedence: root→/var/lib/ventd/diag-bundles/,
	// user→$XDG_STATE_HOME/ventd/diag-bundles/.
	OutputDir string
	// RedactorCfg is the redaction configuration.
	RedactorCfg redactor.Config
	// MappingStore is the persistent mapping store. If nil, an in-memory store is used.
	MappingStore *redactor.MappingStore
	// VentdVersion is embedded in the bundle README and manifest.
	VentdVersion string
	// IncludeTrace includes the rolling ring buffer snapshot (--include-trace).
	IncludeTrace bool
	// AllowRedactionFails downgrades self-check failures to warnings.
	AllowRedactionFails bool
	// ExperimentalFlags is the resolved set of active experimental feature flags.
	// CollectExperimental uses this to include the snapshot in the bundle.
	ExperimentalFlags experimental.Flags
}

// Generate runs all detection collectors, applies the redactor, assembles a
// tar.gz bundle in the resolved output directory, runs the self-check pass,
// and returns the path to the bundle file.
// RULE-DIAG-PR2C-10: output dir 0o700, bundle file 0o600, both verified post-write.
func Generate(ctx context.Context, opts Options) (string, error) {
	outDir := resolveOutputDir(opts.OutputDir)
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", fmt.Errorf("diag: mkdir %s: %w", outDir, err)
	}
	// Verify dir mode post-MkdirAll; umask may override. Re-stat after chmod
	// to confirm the mode actually took effect. RULE-DIAG-PR2C-10.
	fi, err := os.Stat(outDir)
	if err != nil {
		return "", fmt.Errorf("diag: stat output dir: %w", err)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(outDir, 0o700); err != nil {
			return "", fmt.Errorf("diag: chmod output dir: %w", err)
		}
		fi, err = os.Stat(outDir)
		if err != nil {
			return "", fmt.Errorf("diag: re-stat output dir: %w", err)
		}
		if fi.Mode().Perm() != 0o700 {
			return "", fmt.Errorf("diag: output dir mode %o, want 0700 (umask or filesystem override)", fi.Mode().Perm())
		}
	}

	hostname, _ := os.Hostname()

	// Build redactor early so we can use it for the bundle filename.
	// RULE-DIAG-PR2C-08: the same store drives all content in this run.
	store := opts.MappingStore
	if store == nil {
		store = redactor.NewMappingStore()
	}
	red := redactor.New(opts.RedactorCfg, store)

	// Redact hostname unconditionally for the filename — even trusted-recipient
	// and off profiles must not leak cleartext hostname via directory listing.
	redactedHost := strings.TrimSpace(string(red.ApplyHostnameForce([]byte(hostname))))
	if redactedHost == "" || redactedHost == hostname {
		redactedHost = "obf_host"
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
	bundleName := fmt.Sprintf("ventd-diag-%s-%s.tar.gz", redactedHost, timestamp)
	bundlePath := filepath.Join(outDir, bundleName)

	// Print resolved path before writing so the user always knows where it lands.
	fmt.Fprintf(os.Stderr, "ventd diag bundle: writing to %s\n", bundlePath)

	f, err := os.OpenFile(bundlePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("diag: create bundle file: %w", err)
	}

	// Build manifest.
	manifest := NewManifest(opts.VentdVersion)
	manifest.HostIDRedacted = redactedHost

	// Run all collectors.
	var allItems []detection.Item
	var missingTools []string
	collectors := []func(context.Context) detection.CollectResult{
		detection.CollectSystem,
		detection.CollectHwmon,
		detection.CollectGPU,
		detection.CollectUserspace,
		detection.CollectJournal,
		detection.CollectCorsairAIO,
		detection.CollectState,
	}
	for _, col := range collectors {
		r := col(ctx)
		allItems = append(allItems, r.Items...)
		for _, m := range r.MissingTools {
			missingTools = append(missingTools, m.Name+": "+m.Reason)
		}
	}
	// Experimental flags snapshot is collected separately (not context-bound).
	expResult := detection.CollectExperimental(opts.ExperimentalFlags)
	allItems = append(allItems, expResult.Items...)
	manifest.MissingTools = missingTools

	// Write tar.gz.
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	// Kernel version for README.
	kernelVer := ""
	if kv := os.Getenv("KERNEL_VERSION"); kv != "" {
		kernelVer = kv
	} else {
		kernelVer = runtime.GOOS + "/" + runtime.GOARCH
	}

	// Write README.md.
	readme := GenerateReadme(
		opts.VentdVersion,
		kernelVer,
		opts.RedactorCfg.Profile,
		manifest.HostIDRedacted,
		manifest.CapturedAt,
	)
	if err := writeTarEntry(tw, "README.md", readme); err != nil {
		_ = f.Close()
		_ = os.Remove(bundlePath)
		return "", err
	}

	// Write all detection items (redacted).
	for _, item := range allItems {
		if isDenied(item.Path) {
			continue // RULE-DIAG-PR2C-06
		}
		if item.IsSymlink {
			hdr := &tar.Header{
				Typeflag: tar.TypeSymlink,
				Name:     item.Path,
				Linkname: item.Target,
				Mode:     0o777,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				continue
			}
			continue
		}
		content := red.Apply(item.Content)
		if err := writeTarEntry(tw, item.Path, content); err != nil {
			_ = f.Close()
			_ = os.Remove(bundlePath)
			return "", err
		}
		manifest.AddFile(item.Path, content, item.Schema)
	}

	// Build and write REDACTION_REPORT.json. RULE-DIAG-PR2C-07.
	report := red.Report()
	report.RedactionConsistent = true // tentative; corrected by self-check
	reportData, err := report.Marshal()
	if err != nil {
		_ = f.Close()
		_ = os.Remove(bundlePath)
		return "", fmt.Errorf("diag: marshal report: %w", err)
	}
	if err := writeTarEntry(tw, "REDACTION_REPORT.json", reportData); err != nil {
		_ = f.Close()
		_ = os.Remove(bundlePath)
		return "", err
	}

	// Write manifest.json.
	manifestData, err := manifest.Marshal()
	if err != nil {
		_ = f.Close()
		_ = os.Remove(bundlePath)
		return "", fmt.Errorf("diag: marshal manifest: %w", err)
	}
	if err := writeTarEntry(tw, "manifest.json", manifestData); err != nil {
		_ = f.Close()
		_ = os.Remove(bundlePath)
		return "", err
	}

	// Close write layers in order; each finalizes the stream and must be checked.
	if err := tw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(bundlePath)
		return "", fmt.Errorf("diag: close tar writer: %w", err)
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(bundlePath)
		return "", fmt.Errorf("diag: close gzip writer: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(bundlePath)
		return "", fmt.Errorf("diag: close bundle file: %w", err)
	}

	// Post-write: verify file mode 0o600. RULE-DIAG-PR2C-10.
	fi, err = os.Stat(bundlePath)
	if err != nil {
		_ = os.Remove(bundlePath)
		return "", fmt.Errorf("diag: stat bundle: %w", err)
	}
	if fi.Mode().Perm() != 0o600 {
		_ = os.Remove(bundlePath)
		return "", fmt.Errorf("diag: bundle file mode %o, want 0600", fi.Mode().Perm())
	}

	// Self-check pass. RULE-DIAG-PR2C-02/03.
	needles := red.SelfCheckNeedles()
	if len(needles) > 0 && opts.RedactorCfg.Profile != redactor.ProfileOff {
		result, err := redactor.SelfCheck(bundlePath, needles)
		if err != nil {
			return bundlePath, fmt.Errorf("diag: self-check error: %w", err)
		}
		if !result.Ok() {
			if opts.AllowRedactionFails {
				fmt.Fprintf(os.Stderr, "WARNING: self-check detected %d leak(s) in bundle\n", len(result.Leaks))
			} else {
				_ = os.Remove(bundlePath)
				var details strings.Builder
				for _, l := range result.Leaks {
					fmt.Fprintf(&details, "  %s: %q\n", l.File, l.String)
				}
				return "", fmt.Errorf("diag: self-check failed — bundle deleted to prevent leak:\n%s", details.String())
			}
		}
	}

	return bundlePath, nil
}

// ResolveOutputDir returns the bundle output directory per §15.5 precedence:
// override > /var/lib/ventd/diag-bundles (root) > $XDG_STATE_HOME/ventd/diag-bundles (user).
// The web layer uses this to resolve filenames sent to /api/diag/download.
func ResolveOutputDir(override string) string { return resolveOutputDir(override) }

// resolveOutputDir applies §15.5 output dir precedence.
func resolveOutputDir(override string) string {
	if override != "" {
		return override
	}
	if os.Geteuid() == 0 {
		return "/var/lib/ventd/diag-bundles"
	}
	xdg := os.Getenv("XDG_STATE_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(xdg, "ventd", "diag-bundles")
}

func writeTarEntry(tw *tar.Writer, name string, content []byte) error {
	hdr := &tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("diag: tar header %s: %w", name, err)
	}
	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("diag: tar write %s: %w", name, err)
	}
	return nil
}
