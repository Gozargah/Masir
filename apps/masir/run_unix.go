//go:build !windows

package main

import masird "github.com/Gozargah/Masir/libs/masir-daemon"

func runDaemon(opts masird.Options) error { return masird.Run(opts) }
