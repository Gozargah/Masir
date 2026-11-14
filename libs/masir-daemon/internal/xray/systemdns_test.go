package xray

import (
	"context"
	"errors"
	"testing"
)

type fakeSystemDNS struct {
	starts, stops int
	failNext      bool
}

func (d *fakeSystemDNS) before(*Profile) error { return nil }
func (d *fakeSystemDNS) after(context.Context, *Profile) error {
	d.starts++
	if d.failNext {
		d.failNext = false
		return errors.New("DNS failed")
	}
	return nil
}
func (d *fakeSystemDNS) close() error { d.stops++; return nil }
func TestSystemDNSFailureRollsBackRunningProfile(t *testing.T) {
	p := profile(t, "", baseOutbounds, "")
	c := startCore(t, p)
	d := &fakeSystemDNS{failNext: true}
	c.systemDNS = d
	next := profile(t, "", baseOutbounds, `{"ruleTag":"new","port":80,"outboundTag":"block"}`)
	if err := c.Apply(context.Background(), next); err == nil {
		t.Fatal("DNS failure hidden")
	}
	if c.cur != p || !c.Running() {
		t.Fatal("previous profile not restored")
	}
	if d.starts != 2 || d.stops < 1 {
		t.Fatalf("DNS lifecycle: %+v", d)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestInitialSystemDNSFailureStopsXray(t *testing.T) {
	c := New("")
	d := &fakeSystemDNS{failNext: true}
	c.systemDNS = d
	if err := c.Apply(context.Background(), profile(t, "", baseOutbounds, "")); err == nil {
		t.Fatal("DNS failure hidden")
	}
	if c.Running() {
		t.Fatal("Xray left running after initial DNS failure")
	}
	if d.stops < 1 {
		t.Fatal("DNS not cleaned up")
	}
}
