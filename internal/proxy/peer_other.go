//go:build !linux

package proxy

import (
	"errors"
	"net"
)

// PeerUID is only available on Linux, where the proxy runs.
func PeerUID(net.Conn) (int, error) {
	return 0, errors.New("peer credentials need Linux")
}
