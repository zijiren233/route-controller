//go:build !linux

package discovery

import (
	"errors"
	"net/netip"
)

type unsupportedNetwork struct{}

func NewSystemNetwork() Network {
	return unsupportedNetwork{}
}

func (unsupportedNetwork) InterfaceFor(_ netip.Addr) (string, error) {
	return "", errors.New("automatic route discovery requires Linux")
}

func (unsupportedNetwork) ConnectedPrefixes(_ string) ([]netip.Prefix, error) {
	return nil, errors.New("automatic route discovery requires Linux")
}
