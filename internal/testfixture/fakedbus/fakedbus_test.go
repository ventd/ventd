package fakedbus

import "testing"

func TestNew(t *testing.T) {
	_ = New(t, nil)
}
