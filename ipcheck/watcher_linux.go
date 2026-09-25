//go:build linux

package ipcheck

import (
	"context"
	"syscall"
)

const (
	rtmgrpLink       = 1
	rtmgrpIPV4IFAddr = 0x10
	rtmgrpIPV4Route  = 0x40
	rtmgrpIPV6IFAddr = 0x100
	rtmgrpIPV6Route  = 0x400
)

func listenOSNetworkChanges(ctx context.Context, notify chan<- struct{}) {
	defer close(notify)

	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return
	}
	defer syscall.Close(fd)

	sa := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: rtmgrpLink | rtmgrpIPV4IFAddr | rtmgrpIPV4Route | rtmgrpIPV6IFAddr | rtmgrpIPV6Route,
	}
	if err := syscall.Bind(fd, sa); err != nil {
		return
	}

	go func() {
		<-ctx.Done()
		syscall.Close(fd)
	}()

	buf := make([]byte, 4096)
	for {
		nr, from, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		if from != nil && nr > 0 {
			select {
			case notify <- struct{}{}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}
}
