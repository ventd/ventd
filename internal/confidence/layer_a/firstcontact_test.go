package layer_a

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestFirstContact_PersistedPerLifetime binds
// RULE-CONFA-FIRSTCONTACT-01: SeenFirstContact is persisted across
// daemon restarts and re-armed only when the calibration KV
// namespace is wiped (i.e. when the on-disk bucket is removed).
func TestFirstContact_PersistedPerLifetime(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(1_000_000, 0)

	// Phase 1: admit + mark first contact + save.
	e, _ := New(Config{})
	_ = e.Admit("ch1", TierRPMTach, 0, t0)
	e.MarkFirstContact("ch1", t0)
	if s := e.Read("ch1"); !s.SeenFirstContact {
		t.Fatal("SeenFirstContact was not set after MarkFirstContact")
	}
	if err := e.Save(dir, "fp"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Phase 2: fresh estimator, load — flag must persist.
	e2, _ := New(Config{})
	loaded, err := e2.LoadChannel(dir, "ch1", "fp", newSilentLogger())
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if !loaded {
		t.Fatal("LoadChannel returned !loaded after a clean Save")
	}
	s := e2.Read("ch1")
	if s == nil {
		t.Fatal("Read returned nil after LoadChannel")
	}
	if !s.SeenFirstContact {
		t.Error("SeenFirstContact did not survive Save/Load — first-contact invariant lost")
	}

	// Phase 3: simulate KV wipe by deleting the bucket file. A fresh
	// Estimator that LoadChannel'd a missing file MUST NOT carry the
	// flag forward — the channel re-arms its first-contact gate.
	path := bucketPath(dir, "ch1")
	if err := removeIfExists(path); err != nil {
		t.Fatal(err)
	}
	e3, _ := New(Config{})
	loaded, err = e3.LoadChannel(dir, "ch1", "fp", newSilentLogger())
	if err != nil {
		t.Fatalf("LoadChannel after wipe: %v", err)
	}
	if loaded {
		t.Error("LoadChannel returned loaded=true after wipe")
	}
	// Now admit fresh and confirm SeenFirstContact starts false.
	_ = e3.Admit("ch1", TierRPMTach, 0, t0)
	s = e3.Read("ch1")
	if s.SeenFirstContact {
		t.Error("fresh-admit channel reports SeenFirstContact=true — re-arm failed")
	}
}

// TestFirstContact_IdempotentMark verifies that calling
// MarkFirstContact more than once is harmless — the flag stays set
// once and the publish doesn't churn unexpected state.
func TestFirstContact_IdempotentMark(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	_ = e.Admit("ch1", TierRPMTach, 0, t0)
	e.MarkFirstContact("ch1", t0)
	e.MarkFirstContact("ch1", t0)
	e.MarkFirstContact("ch1", t0)
	s := e.Read("ch1")
	if !s.SeenFirstContact {
		t.Fatal("SeenFirstContact should remain set after repeated MarkFirstContact")
	}
}

// TestFirstContact_UnknownChannelIsNoop verifies MarkFirstContact on
// an unknown channelID is a no-op and does NOT auto-admit. Callers
// must Admit + Observe before MarkFirstContact has any effect — the
// controller's flow is "tick → emit observation → if w_pred just
// crossed 0, MarkFirstContact" so the channel always exists by then.
func TestFirstContact_UnknownChannelIsNoop(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	e.MarkFirstContact("never-admitted", t0)
	if got := e.Read("never-admitted"); got != nil {
		t.Errorf("MarkFirstContact silently auto-admitted: %+v", got)
	}
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
