//go:build windows

package main

import (
	"context"
	"net"
	"syscall"
)

func listenWithReuseAddr(network, address string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var sockErr error
			err := c.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			})
			if err != nil {
				return err
			}
			return sockErr
		},
	}
	return lc.Listen(context.Background(), network, address)
}
