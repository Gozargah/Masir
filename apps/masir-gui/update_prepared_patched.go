//go:build mygo_patched

package main

import (
	"context"
	"github.com/egoist/mygo"
)

func preparedUpdateSupported() bool { return true }
func installPreparedUpdate(ctx context.Context, u *mygo.Update, progress func(int64, int64), prepare func(string) error) error {
	return u.InstallWithPrepared(ctx, progress, prepare)
}
