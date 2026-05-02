package hwmon

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNoPWMChannelsAppeared is returned by stepVerify when the driver loaded
// without error but no controllable PWM channels appeared in sysfs within
// the poll window — the canonical signal of a chip-mismatch (loaded the
// wrong OOT module for the actual Super-I/O on the board). The probe-then-
// pick driver-selection loop in internal/setup uses errors.Is to distinguish
// this retryable outcome from real install failures (build errors, missing
// kernel headers, ACPI resource conflicts), where trying the next candidate
// driver would not help.
var ErrNoPWMChannelsAppeared = errors.New("driver installed but no controllable fan channels appeared")

// InstallSteps drives the controlled install pipeline. Each step has an
// explicit success criterion, an explicit cleanup contract, and is tested
// in isolation. The sequence replaces the wrapped `make install` call that
// previously bundled cp + depmod + modprobe atomically — a bundling that
// made it impossible to interleave the deferred-signing step required for
// Secure Boot enforcing systems.
//
// Steps:
//
//	 1. Build         (existing `make` invocation, unchanged)
//	 2. SignBuildDir  (only when SecureBoot enforcing AND key pair present)
//	 3. Copy          (.ko → /lib/modules/<release>/extra/)
//	 4. SignInstalled (belt-and-braces; some distros' make install rebuilds)
//	 5. Depmod        (error-CHECKED unlike the old swallowed call)
//	 6. RegisterDKMS  (existing best-effort helper, unchanged)
//	 7. Modprobe      (now strictly after signing; ACPI conflict path
//	                   preserved from install.go)
//	 8. Persist       (existing persistModule helper)
//	 9. Verify        (extracted PWM-channel-appeared loop)
//
// The pipeline is invoked from InstallDriver via RunPipeline; tests
// substitute fake step funcs to exercise individual contracts.

// PipelineConfig captures the single-driver inputs to the pipeline. Created
// once by InstallDriver from the resolved DriverNeed; all step functions
// read from it rather than threading args through a long signature.
type PipelineConfig struct {
	Driver  DriverNeed
	RepoDir string
	Release string
	Logger  *slog.Logger
	Log     func(string)

	// SecureBootEnforcing is the cached probe result from the preflight
	// run. The pipeline does not re-probe — the preflight is the single
	// source of truth, and re-probing here would produce inconsistent
	// behaviour with what the wizard's status banner already showed.
	SecureBootEnforcing bool

	// MOKKey is non-zero when SecureBootEnforcing is true and the
	// preflight located a usable key pair. The signing steps short-circuit
	// when SecureBootEnforcing is false.
	MOKKey MOKKeyPair
}

// RunPipeline executes the controlled install sequence. Returns a typed
// error on the first step failure so the caller (InstallDriver) can
// dispatch to ErrRebootRequired or surface a generic failure.
func RunPipeline(c PipelineConfig) error {
	if c.Driver.Module == "" {
		return fmt.Errorf("hwmon: pipeline: driver module name is empty")
	}
	if c.Log == nil {
		c.Log = func(string) {}
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}

	c.Log("Building driver (this may take a minute)...")
	if err := stepBuild(c); err != nil {
		return fmt.Errorf("step build: %w", err)
	}

	if c.SecureBootEnforcing {
		c.Log("Signing built modules with MOK key...")
		if err := stepSignBuildDir(c); err != nil {
			return fmt.Errorf("step sign-build-dir: %w", err)
		}
	}

	c.Log("Installing driver to /lib/modules...")
	installed, err := stepCopyToModulesDir(c)
	if err != nil {
		return fmt.Errorf("step copy: %w", err)
	}

	if c.SecureBootEnforcing {
		c.Log("Verifying installed module signatures...")
		if err := stepSignInstalled(c, installed); err != nil {
			return fmt.Errorf("step sign-installed: %w", err)
		}
	}

	c.Log("Updating module index...")
	if err := stepDepmod(c); err != nil {
		// depmod failure is fatal here, unlike the legacy swallowed
		// behaviour (install.go:95 — the old code logged and continued).
		// A failed depmod means modprobe will not find the module by
		// name, so loading will fail with a misleading "module not
		// found" error. Surface the real cause.
		return fmt.Errorf("step depmod: %w", err)
	}

	// DKMS registration remains best-effort — DKMS is a "module survives
	// kernel upgrades" feature, not a "module loads now" requirement.
	registerDKMS(c.RepoDir, c.Driver, c.Log, c.Logger)

	c.Log("Loading driver...")
	if err := stepModprobe(c); err != nil {
		// stepModprobe handles ErrRebootRequired internally and returns
		// it unwrapped; pass through.
		return err
	}

	// stepVerify before stepPersist: probe-then-pick (#822 follow-up) needs
	// stepPersist NOT to fire on a chip-mismatch failure, otherwise the
	// wrongly-bound module would be written to /etc/modules-load.d and
	// auto-reload at boot — undoing the loop's choice. Persisting only on
	// successful PWM-channel appearance keeps the modules-load.d entry in
	// sync with what actually controls fans on this hardware.
	c.Log("Verifying fan controller channels...")
	if err := stepVerify(c); err != nil {
		return fmt.Errorf("step verify: %w", err)
	}

	if err := stepPersist(c); err != nil {
		// Persist failure is logged but doesn't fail the pipeline — the
		// module is loaded and working; persistence affects survival
		// across reboots, not current operation.
		c.Logger.Warn("could not persist module after install",
			"module", c.Driver.Module, "err", err)
	}

	c.Log(fmt.Sprintf("Driver %s installed successfully.", c.Driver.ChipName))
	return nil
}

// stepBuild runs `make` in the driver source tree. The upstream Makefile's
// build target is the only one we use — `make install` was the source of
// the cp+depmod+modprobe atomicity that motivated this rewrite.
func stepBuild(c PipelineConfig) error {
	return runLogDir(c.RepoDir, c.Log, "make")
}

// stepSignBuildDir signs every *.ko under the build tree before copy. We
// sign here (pre-copy) so the .ko under /lib/modules/extra is signed
// from the moment it lands; otherwise a race between copy and modprobe
// could attempt to load an unsigned module.
func stepSignBuildDir(c PipelineConfig) error {
	signed, err := SignBuildDirModules(c.RepoDir, c.Release, c.MOKKey)
	if err != nil {
		return err
	}
	c.Log(fmt.Sprintf("Signed %d module file(s) in build dir.", signed))
	return nil
}

// stepCopyToModulesDir copies every *.ko in the build dir to
// /lib/modules/<release>/extra/. Returns the destination paths of the
// copied modules so the post-copy sign step knows which files to verify.
func stepCopyToModulesDir(c PipelineConfig) ([]string, error) {
	destDir := filepath.Join("/lib/modules", c.Release, "extra")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", destDir, err)
	}
	var installed []string
	walkErr := filepath.Walk(c.RepoDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".ko") {
			return nil
		}
		dest := filepath.Join(destDir, info.Name())
		if err := copyFile(path, dest); err != nil {
			return fmt.Errorf("copy %s → %s: %w", path, dest, err)
		}
		installed = append(installed, dest)
		c.Log(fmt.Sprintf("Installed %s", dest))
		return nil
	})
	if walkErr != nil {
		return installed, walkErr
	}
	if len(installed) == 0 {
		return nil, fmt.Errorf("no .ko files produced by build")
	}
	return installed, nil
}

// stepSignInstalled re-signs the installed .ko files. This is belt-and-
// braces against pipelines that rebuild during install (some upstream
// Makefiles' install target re-invokes the compiler). With our split,
// step 1 produced and signed; step 3 copied; this step verifies the
// destination .ko is signed and re-signs if not.
//
// The sign-file helper is idempotent — signing an already-signed module
// is a no-op that returns 0.
func stepSignInstalled(c PipelineConfig, installed []string) error {
	for _, dest := range installed {
		if err := SignModuleFile(dest, c.Release, c.MOKKey); err != nil {
			return err
		}
	}
	return nil
}

// stepDepmod runs `depmod -a` and returns its error. The legacy
// install.go:94 swallowed this error; the new pipeline surfaces it
// because a failed depmod is the cause of the most common
// "module not found" surprise.
func stepDepmod(c PipelineConfig) error {
	name, args := rootArgv("depmod", []string{"-a"})
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("depmod -a: %w (output: %s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// stepModprobe loads the module. Re-uses the ACPI-resource-conflict
// handling that the legacy install.go path implemented — this preserves
// the auto-patch-bootloader recovery path that handles the it8688e
// "resource busy" failure mode without breaking the install flow.
func stepModprobe(c PipelineConfig) error {
	// Unload any previous failed attempt first so re-runs are idempotent.
	{
		n, a := rootArgv("modprobe", []string{"-r", c.Driver.Module})
		_ = exec.Command(n, a...).Run()
	}
	mpName, mpArgs := rootArgv("modprobe", []string{c.Driver.Module})
	out, err := exec.Command(mpName, mpArgs...).CombinedOutput()
	if err == nil {
		return nil
	}
	outStr := strings.TrimSpace(string(out))
	if strings.Contains(outStr, "resource busy") {
		// ACPI has claimed the fan controller's I/O ports.
		c.Log("ACPI resource conflict detected — updating boot configuration...")
		manualInstr, bootErr := addKernelParam("acpi_enforce_resources=lax", c.Log)
		if bootErr != nil {
			c.Logger.Warn("could not auto-patch bootloader", "err", bootErr)
			return &ErrRebootRequired{
				Message: "Your system firmware (ACPI) has reserved the " + c.Driver.ChipName +
					" fan controller's hardware ports. Auto-patching the bootloader failed (" + bootErr.Error() + "). " +
					manualInstr + " Then reboot.",
			}
		}
		return &ErrRebootRequired{
			Message: "Your system firmware (ACPI) had reserved the " + c.Driver.ChipName +
				" fan controller's hardware ports. We've updated your boot configuration to fix this. " +
				"Click Reboot Now to continue — setup will resume automatically after reboot.",
		}
	}
	return fmt.Errorf("modprobe %s: %w (output: %s)", c.Driver.Module, err, outStr)
}

// stepPersist writes /etc/modules-load.d/ventd.conf so the module loads at
// boot. Reuses the existing persistModule helper from autoload.go.
func stepPersist(c PipelineConfig) error {
	return persistModule(c.Driver.Module, "")
}

// stepVerify polls findPWMPaths up to 6 times at 250ms intervals, looking
// for at least one controllable PWM. Extracted from install.go:131-142;
// keeps the same timing so existing test fixtures don't need to change.
//
// On failure the returned error wraps ErrNoPWMChannelsAppeared so the
// probe-then-pick caller in internal/setup can detect the chip-mismatch
// case and retry with the next driver candidate. Real install failures
// (build, headers, ACPI) surface separately at higher levels and don't
// reach this point.
func stepVerify(c PipelineConfig) error {
	var pwmPaths []string
	for i := 0; i < 6; i++ {
		time.Sleep(250 * time.Millisecond)
		pwmPaths = findPWMPaths()
		if countControllablePWM(pwmPaths) > 0 {
			c.Log(fmt.Sprintf("Found %d controllable fan channel(s).",
				countControllablePWM(pwmPaths)))
			return nil
		}
	}
	return fmt.Errorf("%w (chip-mismatch — your board may use a different chip variant)", ErrNoPWMChannelsAppeared)
}

// copyFile is a small portable file-copy helper. The upstream Makefile's
// install target uses `install` or `cp`; we reproduce the bytes-only copy
// without preserving owner/group/timestamps because /lib/modules entries
// are owned by root and re-stat'd by depmod.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
