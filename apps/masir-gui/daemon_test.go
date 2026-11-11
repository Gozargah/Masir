package main

import (
	"testing"
	"time"
)

// A broken daemon makes the watch retry for as long as the app runs, so the backoff
// has to stay bounded: the shift is capped before it can overflow a time.Duration to
// a negative value, which sleep would treat as no wait at all.
func TestDaemonRetryDelayStaysBounded(t *testing.T) {
	want := []time.Duration{
		3 * time.Second,
		6 * time.Second,
		12 * time.Second,
		24 * time.Second,
		30 * time.Second,
	}
	for i, w := range want {
		if got := retryDelay(i + 1); got != w {
			t.Errorf("retryDelay(%d) = %v, want %v", i+1, got, w)
		}
	}
	for _, failed := range []int{6, 10, 33, 64, 1000, 1 << 20} {
		got := retryDelay(failed)
		if got != 30*time.Second {
			t.Errorf("retryDelay(%d) = %v, want the 30s cap", failed, got)
		}
	}
}
