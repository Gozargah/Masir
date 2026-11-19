//go:build !linux

package main

import (
	"context"
	"errors"
)

// Only Linux packages are installed this way; MyGo updates the app elsewhere.

var errNoPackageUpdates = errors.New("package updates are Linux only")

func platformInstallBlock() installBlock { return blockNone }

func platformFetchPackage(context.Context, packageFetch, func(done, total int64)) (packageFiles, error) {
	return packageFiles{}, errNoPackageUpdates
}

func platformHelperTrusted() (string, error) { return "", errNoPackageUpdates }

func platformInstallPackage(string, packageFiles, int, func()) error { return errNoPackageUpdates }

func retryPackagedServiceUnit() error { return errNoPackageUpdates }
