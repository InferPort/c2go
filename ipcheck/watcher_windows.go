//go:build windows

package ipcheck

import (
	"context"
	"syscall"
	"time"
	"unsafe"
)

var (
	modiphlpapi         = syscall.NewLazyDLL("iphlpapi.dll")
	procNotifyAddrChange = modiphlpapi.NewProc("NotifyAddrChange")
)

func listenOSNetworkChanges(ctx context.Context, notify chan<- struct{}) {
	defer close(notify)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var handle syscall.Handle
		r1, _, _ := procNotifyAddrChange.Call(uintptr(unsafe.Pointer(&handle)), 0)
		if r1 != 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}

		select {
		case notify <- struct{}{}:
		case <-ctx.Done():
			return
		default:
		}
	}
}
