package detectors

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// ContainerEnvFS is the read-only filesystem surface
// ContainerPostbootDetector needs. Production wires the live
// filesystem; tests inject a stub returning canned content.
type ContainerEnvFS interface {
	// FileExists reports whether the path exists. Used for the
	// /.dockerenv flag-file probe.
	FileExists(path string) bool

	// ReadFile returns bytes; os.ErrNotExist on absence. Used for
	// /proc/1/cgroup content-keyword detection.
	ReadFile(path string) ([]byte, error)
}

// liveContainerEnvFS reads the real filesystem.
type liveContainerEnvFS struct{}

func (liveContainerEnvFS) FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
func (liveContainerEnvFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// containerCgroupKeywords are the runtime-name fragments that
// /proc/1/cgroup may include when the daemon's PID 1 is namespaced
// into a container. Mirrors the wizard's preflight probe so the
// detection signal is the same on both surfaces.
var containerCgroupKeywords = []string{"docker", "lxc", "kubepods", "garden", "podman", "containerd"}

// ContainerPostbootDetector re-runs container detection after the
// daemon has been running. Catches the exotic cases where the
// daemon was launched outside a container but its namespace shifted
// — CRIU restore into a container, systemd-nspawn re-enter, or a
// post-startup `nsenter --target 1 ventd` invocation. RULE-IDLE-03
// already hard-refuses calibration in containers; this surfaces the
// runtime case as a Blocker so the operator knows fan control is
// effectively unavailable.
type ContainerPostbootDetector struct {
	// FS is the env reader. Defaults to liveContainerEnvFS{} when nil.
	FS ContainerEnvFS
}

// NewContainerPostbootDetector constructs a detector. fs nil → live
// filesystem.
func NewContainerPostbootDetector(fs ContainerEnvFS) *ContainerPostbootDetector {
	if fs == nil {
		fs = liveContainerEnvFS{}
	}
	return &ContainerPostbootDetector{FS: fs}
}

// Name returns the stable detector ID.
func (d *ContainerPostbootDetector) Name() string { return "container_postboot" }

// Probe scores the four-signal container-detection chain. Two-source
// confirmation per RULE-PROBE-03 prevents false positives — a stale
// /.dockerenv on a reinstalled bare-metal system would otherwise
// fire spuriously.
func (d *ContainerPostbootDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	signals, evidence := scoreContainerSignals(d.FS)
	if signals < 2 {
		return nil, nil
	}

	now := timeNowFromDeps(deps)
	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityBlocker,
		Class:    recovery.ClassContainerised,
		Title:    fmt.Sprintf("Container environment detected post-startup (%d signals)", signals),
		Detail: fmt.Sprintf(
			"Container detection fired with %d independent signals: %s. RULE-IDLE-03 hard-refuses calibration in containers. /sys/class/hwmon writes from inside a container either silently no-op or panic the host kernel driver — the daemon's fan control is effectively unavailable here. If the daemon was migrated into a container deliberately (CRIU, nsenter), restart it on the host instead.",
			signals, strings.Join(evidence, "; "),
		),
		EntityHash: doctor.HashEntity("container_postboot", strings.Join(evidence, ",")),
		Observed:   now,
	}}, nil
}

// scoreContainerSignals checks four independent sources and returns
// (score, evidence-strings). Mirrors RULE-PROBE-03's two-source
// confirmation approach.
func scoreContainerSignals(fs ContainerEnvFS) (int, []string) {
	var score int
	var evidence []string

	if fs.FileExists("/.dockerenv") {
		score++
		evidence = append(evidence, "/.dockerenv exists")
	}

	if data, err := fs.ReadFile("/proc/1/cgroup"); err == nil {
		text := string(data)
		for _, kw := range containerCgroupKeywords {
			if strings.Contains(text, kw) {
				score++
				evidence = append(evidence, fmt.Sprintf("/proc/1/cgroup mentions %q", kw))
				break
			}
		}
	}

	// Overlay-rootfs signal — Docker on cgroup v2 hosts.
	if data, err := fs.ReadFile("/proc/mounts"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[1] == "/" && fields[2] == "overlay" {
				score++
				evidence = append(evidence, "/ is overlay-mounted")
				break
			}
		}
	}

	return score, evidence
}
