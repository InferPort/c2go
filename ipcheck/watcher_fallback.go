//go:build !linux && !darwin && !windows

package ipcheck

import "context"

func listenOSNetworkChanges(ctx context.Context, notify chan<- struct{}) {
	defer close(notify)
	<-ctx.Done()
}
