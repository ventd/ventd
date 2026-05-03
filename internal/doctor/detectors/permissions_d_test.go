package detectors

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
)

// stubPermFS is an in-memory PermissionsFS for tests.
type stubPermFS struct {
	users, groups map[string]bool
	statResults   map[string]stubStatResult
}

type stubStatResult struct {
	info os.FileInfo
	err  error
}

// fakeFileInfo implements os.FileInfo with a fixed mode + isDir.
type fakeFileInfo struct {
	mode  fs.FileMode
	isDir bool
}

func (f fakeFileInfo) Name() string       { return "" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.isDir }
func (f fakeFileInfo) Sys() any           { return nil }

func dirMode(perm fs.FileMode) fakeFileInfo {
	return fakeFileInfo{mode: perm | fs.ModeDir, isDir: true}
}
func fileMode(perm fs.FileMode) fakeFileInfo { return fakeFileInfo{mode: perm} }

func (s *stubPermFS) Stat(name string) (os.FileInfo, error) {
	r, ok := s.statResults[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return r.info, r.err
}
func (s *stubPermFS) LookupUser(name string) bool  { return s.users[name] }
func (s *stubPermFS) LookupGroup(name string) bool { return s.groups[name] }

func freshPermFS() *stubPermFS {
	return &stubPermFS{
		users:  map[string]bool{"ventd": true},
		groups: map[string]bool{"ventd": true},
		statResults: map[string]stubStatResult{
			"/var/lib/ventd":            {info: dirMode(0o755)},
			"/var/lib/ventd/state.yaml": {info: fileMode(0o640)},
		},
	}
}

func TestRULE_DOCTOR_DETECTOR_Permissions_HappyPathNoFacts(t *testing.T) {
	det := NewPermissionsDetector(freshPermFS())

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("happy path emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_Permissions_MissingUserSurfaces(t *testing.T) {
	stub := freshPermFS()
	stub.users = map[string]bool{} // ventd missing

	det := NewPermissionsDetector(stub)
	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for missing ventd user, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "ventd user") {
		t.Errorf("Title doesn't say ventd-user-missing: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_Permissions_MissingGroupSurfaces(t *testing.T) {
	stub := freshPermFS()
	stub.groups = map[string]bool{}

	det := NewPermissionsDetector(stub)
	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Errorf("expected 1 fact for missing ventd group, got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_Permissions_DirModeDriftSurfaces(t *testing.T) {
	stub := freshPermFS()
	stub.statResults["/var/lib/ventd"] = stubStatResult{info: dirMode(0o700)} // wrong

	det := NewPermissionsDetector(stub)
	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for dir mode drift, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "/var/lib/ventd has mode") {
		t.Errorf("Title doesn't mention dir mode: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_Permissions_FileModeDriftSurfaces(t *testing.T) {
	stub := freshPermFS()
	stub.statResults["/var/lib/ventd/state.yaml"] = stubStatResult{info: fileMode(0o644)} // too permissive

	det := NewPermissionsDetector(stub)
	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for file mode drift, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "state.yaml has mode") {
		t.Errorf("Title doesn't mention state.yaml mode: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_Permissions_MissingDirIsBenign(t *testing.T) {
	// /var/lib/ventd absent = first-boot condition (RULE-STATE-10);
	// not a fact.
	stub := freshPermFS()
	delete(stub.statResults, "/var/lib/ventd")
	delete(stub.statResults, "/var/lib/ventd/state.yaml")

	det := NewPermissionsDetector(stub)
	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("missing dir should not surface as fact (first-boot); got %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_Permissions_StatPermissionDeniedSurfaces(t *testing.T) {
	// Unprivileged ventd doctor on a 0700 /var/lib/ventd — Stat
	// returns permission denied. RULE-DOCTOR-04 graceful degrade.
	stub := freshPermFS()
	stub.statResults["/var/lib/ventd"] = stubStatResult{err: errors.New("permission denied")}

	det := NewPermissionsDetector(stub)
	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for permission-denied, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "permission") {
		t.Errorf("Title doesn't mention permission: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_Permissions_RespectsContextCancel(t *testing.T) {
	det := NewPermissionsDetector(freshPermFS())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestLooksUpAccount_PassFormat(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/bash\n" +
		"ventd:x:998:998:ventd daemon:/var/lib/ventd:/usr/sbin/nologin\n" +
		"phoenix:x:1000:1000::/home/phoenix:/bin/bash\n"
	cases := []struct {
		name string
		want bool
	}{
		{"ventd", true},
		{"root", true},
		{"phoenix", true},
		{"ventd-helper", false}, // substring match must NOT fire
		{"", false},
	}
	for _, c := range cases {
		if got := looksUpAccount(passwd, c.name); got != c.want {
			t.Errorf("looksUpAccount(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
