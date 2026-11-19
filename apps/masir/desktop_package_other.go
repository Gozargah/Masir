//go:build !linux

package main

import "github.com/spf13/cobra"

func desktopCommands() []*cobra.Command { return nil }
