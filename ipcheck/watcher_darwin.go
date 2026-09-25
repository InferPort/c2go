//go:build darwin

package ipcheck

import (
	"context"
	"syscall"
)

func listenOSNetworkChanges(ctx context.Context, notify chan<- struct{}) {
	defer close(notify)

	fd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		return
	}
	defer syscall.Close(fd)

	go func() {
		<-ctx.Done()
		syscall.Close(fd)
	}()

	buf := make([]byte, 2048)
	for {
		nr, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		if nr > 0 {
			select {
			case notify <- struct{}{}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}
}
