//go:build !darwin

package service

func LegacyLocation() bool { return false }
