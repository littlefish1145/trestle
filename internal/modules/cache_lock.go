package modules

import (
	"context"
	"trestle/internal/fsx"
)

func lockScanCache(ctx context.Context, path string) (func(), error) {
	lock, err := fsx.WaitLock(ctx, path, "update module scan cache")
	if err != nil {
		return nil, err
	}
	return lock.Close, nil
}
