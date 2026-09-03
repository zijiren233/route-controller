//go:build !linux

package route

import (
	"errors"
	"net/netip"
)

func NewNetlinkBackend(_ string, _, _ int, _, _ netip.Prefix) (Backend, error) {
	return nil, errors.New("the netlink route backend requires Linux")
}
