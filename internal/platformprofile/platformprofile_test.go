package platformprofile

import (
	"testing"
	"testing/fstest"
)

func TestReadAt_Present(t *testing.T) {
	fsys := fstest.MapFS{
		"sys/class/platform-profile/platform-profile-0/choices": &fstest.MapFile{
			Data: []byte("cool quiet balanced performance\n"),
		},
		"sys/class/platform-profile/platform-profile-0/profile": &fstest.MapFile{
			Data: []byte("performance\n"),
		},
	}
	snap, err := ReadAt(fsys, "sys/class/platform-profile")
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !snap.Present {
		t.Fatal("expected Present=true")
	}
	if snap.Current != "performance" {
		t.Errorf("Current: want performance, got %q", snap.Current)
	}
	want := []string{"balanced", "cool", "performance", "quiet"}
	if len(snap.Available) != len(want) {
		t.Fatalf("Available len: want %d, got %d (%v)", len(want), len(snap.Available), snap.Available)
	}
	for i, w := range want {
		if snap.Available[i] != w {
			t.Errorf("Available[%d]: want %q, got %q", i, w, snap.Available[i])
		}
	}
}

func TestReadAt_Absent(t *testing.T) {
	fsys := fstest.MapFS{}
	snap, err := ReadAt(fsys, "sys/class/platform-profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Present {
		t.Error("expected Present=false on empty sysfs")
	}
}

func TestReadAt_NoProfileDirInDir(t *testing.T) {
	fsys := fstest.MapFS{
		"sys/class/platform-profile/.gitkeep": &fstest.MapFile{Data: []byte{}},
	}
	snap, err := ReadAt(fsys, "sys/class/platform-profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Present {
		t.Error("expected Present=false when no platform-profile-N dir matched")
	}
}

func TestReadAt_FirstMatchWins(t *testing.T) {
	fsys := fstest.MapFS{
		"sys/class/platform-profile/platform-profile-0/choices": &fstest.MapFile{
			Data: []byte("low high\n"),
		},
		"sys/class/platform-profile/platform-profile-0/profile": &fstest.MapFile{
			Data: []byte("low\n"),
		},
		"sys/class/platform-profile/platform-profile-1/choices": &fstest.MapFile{
			Data: []byte("a b c\n"),
		},
		"sys/class/platform-profile/platform-profile-1/profile": &fstest.MapFile{
			Data: []byte("a\n"),
		},
	}
	snap, err := ReadAt(fsys, "sys/class/platform-profile")
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if snap.Current != "low" {
		t.Errorf("first-match: want low, got %q", snap.Current)
	}
}
