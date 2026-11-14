//go:build !darwin

package xray

// Other platforms keep Xray's own system-DNS handling.
func (c *Core) EnableSystemDNS() error { return nil }
