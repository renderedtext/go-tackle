package tackle

import (
	"testing"
)

func TestOptionsGetMethods(t *testing.T) {
	opts := &Options{}

	if opts.GetDurable() != true {
		t.Errorf("Expected default Durable to be true, got %v", opts.GetDurable())
	}

	if opts.GetAutoDeleted() != false {
		t.Errorf("Expected default AutoDeleted to be false, got %v", opts.GetAutoDeleted())
	}

	if opts.GetExclusive() != false {
		t.Errorf("Expected default Exclusive to be false, got %v", opts.GetExclusive())
	}
}

func TestOptionsSetValues(t *testing.T) {
	durable := false
	autoDeleted := true
	exclusive := true

	opts := &Options{
		Durable:     &durable,
		AutoDeleted: &autoDeleted,
		Exclusive:   &exclusive,
	}

	if opts.GetDurable() != false {
		t.Errorf("Expected Durable to be false, got %v", opts.GetDurable())
	}

	if opts.GetAutoDeleted() != true {
		t.Errorf("Expected AutoDeleted to be true, got %v", opts.GetAutoDeleted())
	}

	if opts.GetExclusive() != true {
		t.Errorf("Expected Exclusive to be true, got %v", opts.GetExclusive())
	}
}

func TestHelperFunctions(t *testing.T) {
	durablePtr := func(b bool) *bool { return &b }
	autoDeletedPtr := func(b bool) *bool { return &b }
	exclusivePtr := func(b bool) *bool { return &b }

	opts := &Options{
		Durable:     durablePtr(false),
		AutoDeleted: autoDeletedPtr(true),
		Exclusive:   exclusivePtr(true),
	}

	if opts.GetDurable() != false {
		t.Errorf("Expected Durable to be false, got %v", opts.GetDurable())
	}

	if opts.GetAutoDeleted() != true {
		t.Errorf("Expected AutoDeleted to be true, got %v", opts.GetAutoDeleted())
	}

	if opts.GetExclusive() != true {
		t.Errorf("Expected Exclusive to be true, got %v", opts.GetExclusive())
	}
}
