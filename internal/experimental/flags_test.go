package experimental_test

import (
	"reflect"
	"testing"

	"github.com/ventd/ventd/internal/experimental"
)

func TestAll_ReturnsFourNames(t *testing.T) {
	all := experimental.All()
	if len(all) != 4 {
		t.Fatalf("All() returned %d names, want 4", len(all))
	}
	want := []string{"amd_overdrive", "nvidia_coolbits", "ilo4_unlocked", "idrac9_legacy_raw"}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("All() = %v, want %v", all, want)
	}
}

func TestFlags_Active_ReturnsOrderedNames(t *testing.T) {
	f := experimental.Flags{
		AMDOverdrive:    true,
		NVIDIACoolbits:  false,
		ILO4Unlocked:    true,
		IDRAC9LegacyRaw: false,
	}
	got := f.Active()
	want := []string{"amd_overdrive", "ilo4_unlocked"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Active() = %v, want %v", got, want)
	}
}

func TestFlags_Active_Empty(t *testing.T) {
	var f experimental.Flags
	got := f.Active()
	if len(got) != 0 {
		t.Errorf("Active() on zero Flags = %v, want empty slice", got)
	}
}

func TestFlags_Get_KnownAndUnknown(t *testing.T) {
	f := experimental.Flags{AMDOverdrive: true, NVIDIACoolbits: false}

	v, ok := f.Get("amd_overdrive")
	if !ok || !v {
		t.Errorf("Get(amd_overdrive) = (%v,%v), want (true,true)", v, ok)
	}

	v, ok = f.Get("nvidia_coolbits")
	if !ok || v {
		t.Errorf("Get(nvidia_coolbits) = (%v,%v), want (false,true)", v, ok)
	}

	_, ok = f.Get("bogus")
	if ok {
		t.Error("Get(bogus) ok=true, want false")
	}
}
