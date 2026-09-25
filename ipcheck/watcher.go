package ipcheck

import (
	"context"
	"time"
)

// WatchNetworkChanges starts an OS-level listener for network adapter and route changes.
// It returns a channel that emits a signal when network changes are detected,
// debounced to prevent a flurry of rapid events.
func WatchNetworkChanges(ctx context.Context) <-chan struct{} {
	out := make(chan struct{}, 1)
	raw := make(chan struct{}, 10)

	go listenOSNetworkChanges(ctx, raw)

	go func() {
		defer close(out)
		var debounceTimer *time.Timer
		var debounceC <-chan time.Time

		for {
			select {
			case <-ctx.Done():
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				return
			case _, ok := <-raw:
				if !ok {
					return
				}
				if debounceTimer == nil {
					debounceTimer = time.NewTimer(2 * time.Second)
					debounceC = debounceTimer.C
				} else {
					debounceTimer.Reset(2 * time.Second)
				}
			case <-debounceC:
				debounceTimer = nil
				debounceC = nil
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()

	return out
}
