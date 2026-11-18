//go:build !darwin

package xray

func configureProbeInterface(string) error { return nil }
