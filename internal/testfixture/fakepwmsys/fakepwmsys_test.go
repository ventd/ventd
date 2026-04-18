package fakepwmsys

import "testing"

func TestNew(t *testing.T) {
	f := New(t, RPi5())
	if f.Root == "" {
		t.Fatal("Root must not be empty")
	}
	// Verify npwm is readable for both chips.
	for _, rel := range []string{"pwmchip0/npwm", "pwmchip1/npwm"} {
		v, err := f.ReadUint(rel)
		if err != nil {
			t.Fatalf("ReadUint(%s): %v", rel, err)
		}
		if v != 2 {
			t.Fatalf("ReadUint(%s) = %d, want 2", rel, v)
		}
	}
}
