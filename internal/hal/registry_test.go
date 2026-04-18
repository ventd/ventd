package hal

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type fakeBackend struct {
	name     string
	channels []Channel
}

func (b *fakeBackend) Name() string                                   { return b.name }
func (b *fakeBackend) Enumerate(_ context.Context) ([]Channel, error) { return b.channels, nil }
func (b *fakeBackend) Read(_ Channel) (Reading, error)                { return Reading{}, nil }
func (b *fakeBackend) Write(_ Channel, _ uint8) error                 { return nil }
func (b *fakeBackend) Restore(_ Channel) error                        { return nil }
func (b *fakeBackend) Close() error                                   { return nil }

func TestRegistry_RegisterAndBackend(t *testing.T) {
	Reset()
	b := &fakeBackend{name: "fakeA"}
	Register("fakeA", b)
	got, ok := Backend("fakeA")
	if !ok || got != b {
		t.Fatalf("Backend lookup returned (%v, %v), want (b, true)", got, ok)
	}
}

func TestRegistry_Backend_Missing(t *testing.T) {
	Reset()
	_, ok := Backend("nothere")
	if ok {
		t.Fatal("Backend returned ok=true for unregistered name")
	}
}

func TestRegistry_Reset_ClearsRegistry(t *testing.T) {
	Reset()
	Register("x", &fakeBackend{name: "x"})
	Reset()
	if _, ok := Backend("x"); ok {
		t.Fatal("Reset did not clear registry")
	}
}

func TestRegistry_Register_OverwritesSameName(t *testing.T) {
	Reset()
	b1 := &fakeBackend{name: "dup"}
	b2 := &fakeBackend{name: "dup"}
	Register("dup", b1)
	Register("dup", b2)
	got, ok := Backend("dup")
	if !ok {
		t.Fatal("Backend not found after double-register")
	}
	if got != b2 {
		t.Error("second Register should overwrite the first")
	}
}

func TestRegistry_Register_NilOrEmpty_IsNoop(t *testing.T) {
	Reset()
	Register("", &fakeBackend{name: "empty-name"})
	Register("valid", nil)
	if _, ok := Backend(""); ok {
		t.Error("empty-name backend should not be registered")
	}
	if _, ok := Backend("valid"); ok {
		t.Error("nil backend should not be registered")
	}
}

func TestRegistry_Enumerate_AggregatesAllBackends(t *testing.T) {
	Reset()
	Register("one", &fakeBackend{name: "one", channels: []Channel{{ID: "c1"}, {ID: "c2"}}})
	Register("two", &fakeBackend{name: "two", channels: []Channel{{ID: "c3"}}})
	got, err := Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate err: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("Enumerate len = %d, want 3", len(got))
	}
}

func TestRegistry_Enumerate_TagsIDsWithBackendName(t *testing.T) {
	Reset()
	Register("hw", &fakeBackend{name: "hw", channels: []Channel{{ID: "pwm1"}}})
	got, err := Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Enumerate len = %d, want 1", len(got))
	}
	if got[0].ID != "hw:pwm1" {
		t.Errorf("channel ID = %q, want %q", got[0].ID, "hw:pwm1")
	}
}

func TestRegistry_Enumerate_EmptyRegistry(t *testing.T) {
	Reset()
	got, err := Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate on empty registry err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Enumerate on empty registry returned %d channels, want 0", len(got))
	}
}

func TestRegistry_Resolve_Success(t *testing.T) {
	Reset()
	ch := Channel{ID: "pwm1"}
	Register("fb", &fakeBackend{name: "fb", channels: []Channel{ch}})
	b, got, err := Resolve("fb:pwm1")
	if err != nil {
		t.Fatalf("Resolve err: %v", err)
	}
	if b == nil {
		t.Fatal("Resolve returned nil backend")
	}
	if got.ID != "pwm1" {
		t.Errorf("Resolve channel ID = %q, want %q", got.ID, "pwm1")
	}
}

func TestRegistry_Resolve_UnknownBackend(t *testing.T) {
	Reset()
	_, _, err := Resolve("missing:/foo")
	if err == nil {
		t.Fatal("Resolve on unknown backend returned nil error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error message should name the missing backend, got: %v", err)
	}
}

func TestRegistry_Resolve_MalformedKey(t *testing.T) {
	Reset()
	Register("fb", &fakeBackend{name: "fb"})
	_, _, err := Resolve("no-separator-here")
	if err == nil {
		t.Fatal("Resolve on malformed key returned nil error")
	}
}

func TestRegistry_Resolve_ChannelNotInBackend(t *testing.T) {
	Reset()
	Register("fb", &fakeBackend{name: "fb", channels: []Channel{{ID: "pwm1"}}})
	_, _, err := Resolve("fb:pwm99")
	if err == nil {
		t.Fatal("Resolve for non-existent channel returned nil error")
	}
}

func TestRegistry_ConcurrentRegistration_Race(t *testing.T) {
	Reset()
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			Register("fb-"+string(rune('a'+i%26)), &fakeBackend{name: "fb"})
		}()
	}
	wg.Wait()
	// No count assertion — the point is that -race finds no data races on
	// the registry mutex.
}
