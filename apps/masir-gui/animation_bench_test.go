package main

import (
	"testing"
	"time"
)

// BenchmarkConnectedAnimation measures warmed frames with the status dot breathing.
func BenchmarkConnectedAnimation(b *testing.B) {
	f := ukServers()
	f.status.Settings.Power = true
	f.status.XrayRunning = true
	a := newTestApp(f)
	tt := tester(a)
	tt.Advance(5 * time.Second)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Advance(time.Second / 60)
	}
}

// BenchmarkPendingLatency measures the open server list during pending probes.
func BenchmarkPendingLatency(b *testing.B) {
	a := newTestApp(ukServers())
	a.open = sectionServers
	for _, s := range a.subscriptions.servers {
		a.testing[s.key] = true
	}
	tt := tester(a)
	tt.Advance(5 * time.Second)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Advance(time.Second / 60)
	}
}
