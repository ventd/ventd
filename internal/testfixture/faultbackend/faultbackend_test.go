package faultbackend_test

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/testfixture/faultbackend"
)

func TestFaultBackend_NoFaultBehavesLikeAWorkingFan(t *testing.T) {
	b := faultbackend.New("fault", faultbackend.Channel("c0"))
	ch := faultbackend.Channel("c0")

	chans, err := b.Enumerate(context.Background())
	if err != nil || len(chans) != 1 || chans[0].ID != "c0" {
		t.Fatalf("Enumerate = %v, %v; want one channel c0", chans, err)
	}
	if err := b.Write(ch, 142); err != nil {
		t.Fatalf("Write with no fault: %v", err)
	}
	r, err := b.Read(ch)
	if err != nil || !r.OK || r.PWM != 142 {
		t.Errorf("Read = %+v, %v; want OK PWM=142", r, err)
	}
	if v, ok := b.LastPWM("c0"); !ok || v != 142 {
		t.Errorf("LastPWM = %d, %v; want 142, true", v, ok)
	}
}

func TestFaultBackend_WriteErrsConsumedFIFO(t *testing.T) {
	b := faultbackend.New("fault", faultbackend.Channel("c0"))
	b.WriteErrs = []error{syscall.EBUSY, nil, syscall.EIO} // then exhausted → nil
	ch := faultbackend.Channel("c0")

	want := []error{syscall.EBUSY, nil, syscall.EIO, nil}
	for i, w := range want {
		if got := b.Write(ch, uint8(i)); !errors.Is(got, w) {
			t.Errorf("Write #%d = %v, want %v", i, got, w)
		}
	}
	if b.WriteCount("c0") != 4 {
		t.Errorf("WriteCount = %d, want 4", b.WriteCount("c0"))
	}
	// Failed writes (#0 EBUSY, #2 EIO) must not commit; the last successful
	// write was #3 (queue exhausted → pass), which committed pwm=3.
	if v, _ := b.LastPWM("c0"); v != 3 {
		t.Errorf("LastPWM = %d, want 3 (last successful write's duty; failed writes don't commit)", v)
	}
}

func TestFaultBackend_WritePolicyTakesPrecedenceAndCounts(t *testing.T) {
	b := faultbackend.New("fault", faultbackend.Channel("c0"))
	b.WriteErrs = []error{syscall.EIO} // must be ignored once a policy is set
	b.WritePolicy = func(_ string, _ uint8, call int) error {
		if call <= 2 {
			return syscall.EBUSY
		}
		return nil
	}
	ch := faultbackend.Channel("c0")
	for i, want := range []error{syscall.EBUSY, syscall.EBUSY, nil} {
		if got := b.Write(ch, 0); !errors.Is(got, want) {
			t.Errorf("call #%d = %v, want %v (policy by call count, not the FIFO)", i+1, got, want)
		}
	}
}

func TestFaultBackend_RestoreRecordedAndScriptable(t *testing.T) {
	b := faultbackend.New("fault", faultbackend.Channel("c0"))
	b.RestoreErrs = []error{syscall.EINVAL} // first restore fails, rest pass
	ch := faultbackend.Channel("c0")

	if err := b.Restore(ch); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("first Restore = %v, want EINVAL", err)
	}
	if err := b.Restore(ch); err != nil {
		t.Errorf("second Restore = %v, want nil (queue exhausted)", err)
	}
	if b.RestoreCount("c0") != 2 || len(b.Restores) != 2 {
		t.Errorf("Restore bookkeeping: count=%d records=%v, want 2 each", b.RestoreCount("c0"), b.Restores)
	}
}

// AlwaysFail is the storm helper used by the controller tests.
func TestFaultBackend_AlwaysFail(t *testing.T) {
	b := faultbackend.New("fault", faultbackend.Channel("c0"))
	b.WritePolicy = faultbackend.AlwaysFail(syscall.EBUSY)
	ch := faultbackend.Channel("c0")
	for i := 0; i < 5; i++ {
		if !errors.Is(b.Write(ch, 0), syscall.EBUSY) {
			t.Fatalf("AlwaysFail let a write through on attempt %d", i+1)
		}
	}
}

var _ hal.FanBackend = faultbackend.New("x")
