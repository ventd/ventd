package detectors

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

type stubContainerFS struct {
	files map[string]string
	exist map[string]bool
}

func (s *stubContainerFS) FileExists(path string) bool { return s.exist[path] }

func (s *stubContainerFS) ReadFile(path string) ([]byte, error) {
	if v, ok := s.files[path]; ok {
		return []byte(v), nil
	}
	return nil, errFileNotExist
}

func TestRULE_DOCTOR_DETECTOR_ContainerPostboot_BareMetalNoFacts(t *testing.T) {
	det := NewContainerPostbootDetector(&stubContainerFS{
		files: map[string]string{
			"/proc/1/cgroup": "0::/init.scope\n",
			"/proc/mounts":   "rootfs / rootfs rw 0 0\n",
		},
	})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("bare metal emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_ContainerPostboot_DockerWithCgroupV2(t *testing.T) {
	// Docker on cgroup v2: /.dockerenv + overlay rootfs (cgroup
	// itself shows only "0::/" with no container keyword). Two
	// signals → fires.
	det := NewContainerPostbootDetector(&stubContainerFS{
		exist: map[string]bool{"/.dockerenv": true},
		files: map[string]string{
			"/proc/1/cgroup": "0::/\n",
			"/proc/mounts":   "overlay / overlay rw,relatime 0 0\n",
		},
	})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for docker-cgroup-v2, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want Blocker", facts[0].Severity)
	}
}

func TestRULE_DOCTOR_DETECTOR_ContainerPostboot_LXC(t *testing.T) {
	// LXC: /proc/1/cgroup mentions lxc + (no /.dockerenv +
	// possibly no overlay). Need 2 signals — let's exercise the
	// cgroup + overlay combo.
	det := NewContainerPostbootDetector(&stubContainerFS{
		files: map[string]string{
			"/proc/1/cgroup": "12:devices:/lxc/test\n",
			"/proc/mounts":   "overlay / overlay rw 0 0\n",
		},
	})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Errorf("expected 1 fact for lxc + overlay, got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_ContainerPostboot_SingleSignalNoFalsePositive(t *testing.T) {
	// Stale /.dockerenv on a reinstalled bare-metal system —
	// only one signal, must not fire.
	det := NewContainerPostbootDetector(&stubContainerFS{
		exist: map[string]bool{"/.dockerenv": true},
		files: map[string]string{
			"/proc/1/cgroup": "0::/init.scope\n",
			"/proc/mounts":   "rootfs / rootfs rw 0 0\n",
		},
	})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("single-signal emitted facts (false positive): %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_ContainerPostboot_KubernetesPod(t *testing.T) {
	// Kubernetes pod: /proc/1/cgroup mentions kubepods + overlay
	// rootfs. Two signals.
	det := NewContainerPostbootDetector(&stubContainerFS{
		files: map[string]string{
			"/proc/1/cgroup": "0::/kubepods.slice/kubepods-pod123.slice/cri-containerd-456.scope\n",
			"/proc/mounts":   "overlay / overlay rw 0 0\n",
		},
	})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for kubepods, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "Container") {
		t.Errorf("Title doesn't mention container: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_ContainerPostboot_RespectsContextCancel(t *testing.T) {
	det := NewContainerPostbootDetector(&stubContainerFS{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestScoreContainerSignals_AllKeywordsRecognised(t *testing.T) {
	for _, kw := range containerCgroupKeywords {
		fs := &stubContainerFS{
			files: map[string]string{
				"/proc/1/cgroup": "0::/some/" + kw + "/path\n",
			},
		}
		score, _ := scoreContainerSignals(fs)
		if score != 1 {
			t.Errorf("keyword %q: score=%d, want 1", kw, score)
		}
	}
}
