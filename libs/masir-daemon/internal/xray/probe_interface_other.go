//go:build !darwin || !xray_patched

package xray

func configureProbeInterface() error { return nil }
