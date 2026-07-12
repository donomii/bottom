//go:build !linux

package main

import (
	"context"
	"time"
)

func newSystemCorrelationSource(ctx context.Context, interval time.Duration) systemCorrelationSource {
	return systemCorrelationSource{}
}
