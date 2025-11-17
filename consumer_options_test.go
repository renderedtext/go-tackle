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

func TestMaxRetriesOptions(t *testing.T) {
	opts := &Options{}

	if opts.GetMaxRetries() != DefaultRetryLimit {
		t.Errorf("Expected default MaxRetries to be %v, got %v", DefaultRetryLimit, opts.GetMaxRetries())
	}

	maxRetries := int32(5)
	opts.MaxRetries = &maxRetries

	if opts.GetMaxRetries() != 5 {
		t.Errorf("Expected MaxRetries to be 5, got %v", opts.GetMaxRetries())
	}

	zeroRetries := int32(0)
	opts.MaxRetries = &zeroRetries

	if opts.GetMaxRetries() != 0 {
		t.Errorf("Expected MaxRetries to be 0, got %v", opts.GetMaxRetries())
	}
}

func TestDeadQueueOptions(t *testing.T) {
	opts := &Options{}

	if opts.GetEnableDeadQueue() != true {
		t.Errorf("Expected default EnableDeadQueue to be true, got %v", opts.GetEnableDeadQueue())
	}

	enableDeadQueue := false
	opts.EnableDeadQueue = &enableDeadQueue

	if opts.GetEnableDeadQueue() != false {
		t.Errorf("Expected EnableDeadQueue to be false, got %v", opts.GetEnableDeadQueue())
	}

	enableDeadQueueTrue := true
	opts.EnableDeadQueue = &enableDeadQueueTrue

	if opts.GetEnableDeadQueue() != true {
		t.Errorf("Expected EnableDeadQueue to be true, got %v", opts.GetEnableDeadQueue())
	}
}
