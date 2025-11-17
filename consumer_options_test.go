package tackle

import (
	"fmt"
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

func TestRetryAndDeadQueueBehavior(t *testing.T) {
	testCases := []struct {
		name               string
		maxRetries         *int32
		enableDeadQueue    *bool
		expectedMaxRetries int32
		expectedDeadQueue  bool
	}{
		{"Default behavior", nil, nil, DefaultRetryLimit, true},
		{"No retries, dead queue enabled", intPtr(0), nil, 0, true},
		{"No retries, no dead queue", intPtr(0), boolPtr(false), 0, false},
		{"Custom retries, no dead queue", intPtr(3), boolPtr(false), 3, false},
		{"Custom retries, dead queue enabled", intPtr(5), boolPtr(true), 5, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &Options{
				MaxRetries:      tc.maxRetries,
				EnableDeadQueue: tc.enableDeadQueue,
			}

			if opts.GetMaxRetries() != tc.expectedMaxRetries {
				t.Errorf("Expected MaxRetries to be %v, got %v", tc.expectedMaxRetries, opts.GetMaxRetries())
			}

			if opts.GetEnableDeadQueue() != tc.expectedDeadQueue {
				t.Errorf("Expected EnableDeadQueue to be %v, got %v", tc.expectedDeadQueue, opts.GetEnableDeadQueue())
			}
		})
	}
}

func intPtr(i int32) *int32 {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

func TestQueueConfigurationBehavior(t *testing.T) {
	testCases := []struct {
		name             string
		maxRetries       *int32
		enableDeadQueue  *bool
		expectRetryQueue bool
		expectDeadQueue  bool
		errorBehavior    string
	}{
		{"Default behavior", nil, nil, true, true, "retry then dead queue"},
		{"No retries, dead queue enabled", intPtr(0), nil, false, true, "direct to dead queue"},
		{"No retries, no dead queue", intPtr(0), boolPtr(false), false, false, "drop message"},
		{"Custom retries, no dead queue", intPtr(3), boolPtr(false), true, false, "retry then requeue"},
		{"Custom retries, dead queue enabled", intPtr(5), boolPtr(true), true, true, "retry then dead queue"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &Options{
				Service:         "test-service",
				RoutingKey:      "test-key",
				MaxRetries:      tc.maxRetries,
				EnableDeadQueue: tc.enableDeadQueue,
			}

			shouldCreateRetryQueue := opts.GetMaxRetries() > 0
			shouldCreateDeadQueue := opts.GetEnableDeadQueue()

			if shouldCreateRetryQueue != tc.expectRetryQueue {
				t.Errorf("Expected retry queue creation to be %v, got %v", tc.expectRetryQueue, shouldCreateRetryQueue)
			}

			if shouldCreateDeadQueue != tc.expectDeadQueue {
				t.Errorf("Expected dead queue creation to be %v, got %v", tc.expectDeadQueue, shouldCreateDeadQueue)
			}

			// Test queue naming works correctly
			if tc.expectDeadQueue {
				expectedDeadQueue := "test-service.test-key.dead"
				if opts.GetDeadQueueName() != expectedDeadQueue {
					t.Errorf("Expected dead queue name to be %s, got %s", expectedDeadQueue, opts.GetDeadQueueName())
				}
			}

			if tc.expectRetryQueue {
				expectedDelayQueue := "test-service.test-key.delay." + fmt.Sprintf("%d", opts.GetRetryDelay())
				if opts.GetDelayQueueName() != expectedDelayQueue {
					t.Errorf("Expected delay queue name to be %s, got %s", expectedDelayQueue, opts.GetDelayQueueName())
				}
			}
		})
	}
}
