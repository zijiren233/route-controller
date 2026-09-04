//go:build linux

package discovery

import (
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/vishvananda/netlink"
)

type systemNetwork struct{}

func NewSystemNetwork() Network {
	return systemNetwork{}
}

func (systemNetwork) InterfaceFor(address netip.Addr) (string, error) {
	routes, err := netlink.RouteGet(net.IP(address.AsSlice()))
	if err != nil {
		return "", fmt.Errorf("get kernel route: %w", err)
	}

	indexes := make([]int, 0, len(routes))
	for _, route := range routes {
		if route.LinkIndex > 0 {
			indexes = append(indexes, route.LinkIndex)
		}
	}

	slices.Sort(indexes)
	indexes = slices.Compact(indexes)

	if len(indexes) != 1 {
		return "", fmt.Errorf("expected one output interface, found %d", len(indexes))
	}

	link, err := netlink.LinkByIndex(indexes[0])
	if err != nil {
		return "", fmt.Errorf("find output interface index %d: %w", indexes[0], err)
	}

	return link.Attrs().Name, nil
}

func (systemNetwork) ConnectedPrefixes(interfaceName string) ([]netip.Prefix, error) {
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return nil, fmt.Errorf("find interface: %w", err)
	}

	routes, err := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{LinkIndex: link.Attrs().Index},
		netlink.RT_FILTER_OIF,
	)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}

	prefixes := make([]netip.Prefix, 0, len(routes))
	for _, route := range routes {
		if route.Scope != netlink.SCOPE_LINK || route.Dst == nil {
			continue
		}

		prefix, parseErr := netip.ParsePrefix(route.Dst.String())
		if parseErr == nil && prefix.Addr().Is4() {
			prefixes = append(prefixes, prefix.Masked())
		}
	}

	return prefixes, nil
}
