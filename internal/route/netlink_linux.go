//go:build linux

package route

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type netlinkAPI interface {
	LinkByName(name string) (netlink.Link, error)
	RouteListFiltered(family int, filter *netlink.Route, mask uint64) ([]netlink.Route, error)
	RouteReplace(route *netlink.Route) error
	RouteDel(route *netlink.Route) error
}

type systemNetlink struct{}

func (systemNetlink) LinkByName(name string) (netlink.Link, error) {
	return netlink.LinkByName(name)
}

func (systemNetlink) RouteListFiltered(
	family int,
	filter *netlink.Route,
	mask uint64,
) ([]netlink.Route, error) {
	return netlink.RouteListFiltered(family, filter, mask)
}

func (systemNetlink) RouteReplace(route *netlink.Route) error {
	return netlink.RouteReplace(route)
}

func (systemNetlink) RouteDel(route *netlink.Route) error {
	return netlink.RouteDel(route)
}

type NetlinkBackend struct {
	api         netlinkAPI
	linkIndex   int
	table       int
	protocol    netlink.RouteProtocol
	podCIDR     netip.Prefix
	serviceCIDR netip.Prefix
}

func NewNetlinkBackend(
	interfaceName string,
	table int,
	protocol int,
	podCIDR netip.Prefix,
	serviceCIDR netip.Prefix,
) (Backend, error) {
	return newNetlinkBackend(systemNetlink{}, interfaceName, table, protocol, podCIDR, serviceCIDR)
}

func newNetlinkBackend(
	api netlinkAPI,
	interfaceName string,
	table int,
	protocol int,
	podCIDR netip.Prefix,
	serviceCIDR netip.Prefix,
) (*NetlinkBackend, error) {
	link, err := api.LinkByName(interfaceName)
	if err != nil {
		return nil, fmt.Errorf("find interface %q: %w", interfaceName, err)
	}

	return &NetlinkBackend{
		api:         api,
		linkIndex:   link.Attrs().Index,
		table:       table,
		protocol:    netlink.RouteProtocol(protocol),
		podCIDR:     podCIDR,
		serviceCIDR: serviceCIDR,
	}, nil
}

func (backend *NetlinkBackend) Reconcile(
	ctx context.Context,
	desired []Desired,
	options ReconcileOptions,
) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	existing, err := backend.api.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: backend.table},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return Result{}, fmt.Errorf("list routes in table %d: %w", backend.table, err)
	}

	byDestination := indexByDestination(existing)
	if err := backend.checkConflicts(desired, byDestination); err != nil {
		return Result{}, err
	}

	result := Result{Actions: make([]Action, 0)}

	desiredDestinations := make(map[netip.Prefix]struct{}, len(desired))
	for _, wanted := range desired {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		desiredDestinations[wanted.Destination] = struct{}{}

		current := ownedRoute(byDestination[wanted.Destination], backend.protocol)
		if current != nil &&
			sameRoute(*current, wanted, backend.linkIndex, backend.table, backend.protocol) {
			continue
		}

		action := Action{Type: ActionReplace, Destination: wanted.Destination}
		result.Actions = append(result.Actions, action)

		if options.DryRun {
			continue
		}

		if replaceErr := backend.api.RouteReplace(
			buildRoute(wanted, backend.linkIndex, backend.table, backend.protocol),
		); replaceErr != nil {
			return result, fmt.Errorf("%s: %w", action, replaceErr)
		}
	}

	if options.Prune {
		for index := range existing {
			current := &existing[index]

			destination, ok := routeDestination(current)
			if !ok || current.Protocol != backend.protocol ||
				!backend.managedDestination(destination) {
				continue
			}

			if _, wanted := desiredDestinations[destination]; wanted {
				continue
			}

			if err := ctx.Err(); err != nil {
				return result, err
			}

			action := Action{Type: ActionDelete, Destination: destination}
			result.Actions = append(result.Actions, action)

			if options.DryRun {
				continue
			}

			if deleteErr := backend.api.RouteDel(
				current,
			); deleteErr != nil &&
				!errors.Is(deleteErr, unix.ESRCH) {
				return result, fmt.Errorf("%s: %w", action, deleteErr)
			}
		}
	}

	slices.SortFunc(result.Actions, func(first, second Action) int {
		return strings.Compare(first.String(), second.String())
	})

	return result, nil
}

func (backend *NetlinkBackend) checkConflicts(
	desired []Desired,
	byDestination map[netip.Prefix][]netlink.Route,
) error {
	conflicts := make([]string, 0)
	for _, wanted := range desired {
		for _, current := range byDestination[wanted.Destination] {
			if current.Protocol != backend.protocol {
				conflicts = append(
					conflicts,
					fmt.Sprintf(
						"%s already exists with protocol %d",
						wanted.Destination,
						current.Protocol,
					),
				)
			}
		}
	}

	if len(conflicts) == 0 {
		return nil
	}

	slices.Sort(conflicts)
	conflicts = slices.Compact(conflicts)

	return fmt.Errorf("route conflict: %s", strings.Join(conflicts, "; "))
}

func (backend *NetlinkBackend) managedDestination(destination netip.Prefix) bool {
	return destination == backend.serviceCIDR || PrefixWithin(destination, backend.podCIDR)
}

func indexByDestination(routes []netlink.Route) map[netip.Prefix][]netlink.Route {
	indexed := make(map[netip.Prefix][]netlink.Route)
	for _, current := range routes {
		destination, ok := routeDestination(&current)
		if ok {
			indexed[destination] = append(indexed[destination], current)
		}
	}

	return indexed
}

func routeDestination(route *netlink.Route) (netip.Prefix, bool) {
	if route.Dst == nil {
		return netip.Prefix{}, false
	}

	destination, err := netip.ParsePrefix(route.Dst.String())
	if err != nil || !destination.Addr().Is4() {
		return netip.Prefix{}, false
	}

	return destination.Masked(), true
}

func ownedRoute(routes []netlink.Route, protocol netlink.RouteProtocol) *netlink.Route {
	for index := range routes {
		if routes[index].Protocol == protocol {
			return &routes[index]
		}
	}

	return nil
}

func buildRoute(
	wanted Desired,
	linkIndex int,
	table int,
	protocol netlink.RouteProtocol,
) *netlink.Route {
	route := &netlink.Route{
		Dst:       prefixToIPNet(wanted.Destination),
		LinkIndex: linkIndex,
		Table:     table,
		Protocol:  protocol,
		Scope:     netlink.SCOPE_UNIVERSE,
		Type:      unix.RTN_UNICAST,
	}
	if len(wanted.Gateways) == 1 {
		route.Gw = addressToIP(wanted.Gateways[0])
		return route
	}

	route.LinkIndex = 0

	route.MultiPath = make([]*netlink.NexthopInfo, 0, len(wanted.Gateways))
	for _, gateway := range wanted.Gateways {
		route.MultiPath = append(route.MultiPath, &netlink.NexthopInfo{
			LinkIndex: linkIndex,
			Gw:        addressToIP(gateway),
			Hops:      0,
		})
	}

	return route
}

func sameRoute(
	current netlink.Route,
	wanted Desired,
	linkIndex int,
	table int,
	protocol netlink.RouteProtocol,
) bool {
	destination, ok := routeDestination(&current)
	if !ok || destination != wanted.Destination || current.Table != table ||
		current.Protocol != protocol || current.Scope != netlink.SCOPE_UNIVERSE ||
		current.Type != unix.RTN_UNICAST {
		return false
	}

	actual := routeGateways(current, linkIndex)

	expected := append([]netip.Addr(nil), wanted.Gateways...)

	slices.SortFunc(actual, netip.Addr.Compare)
	slices.SortFunc(expected, netip.Addr.Compare)

	return slices.Equal(actual, expected)
}

func routeGateways(current netlink.Route, linkIndex int) []netip.Addr {
	gateways := make([]netip.Addr, 0, len(current.MultiPath)+1)
	if current.Gw != nil && current.LinkIndex == linkIndex {
		if gateway, ok := netip.AddrFromSlice(current.Gw); ok {
			gateways = append(gateways, gateway.Unmap())
		}
	}

	for _, nextHop := range current.MultiPath {
		if nextHop.Gw == nil || nextHop.LinkIndex != linkIndex {
			continue
		}

		if gateway, ok := netip.AddrFromSlice(nextHop.Gw); ok {
			gateways = append(gateways, gateway.Unmap())
		}
	}

	return gateways
}

func prefixToIPNet(prefix netip.Prefix) *net.IPNet {
	return &net.IPNet{
		IP:   addressToIP(prefix.Addr()),
		Mask: net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen()),
	}
}

func addressToIP(address netip.Addr) net.IP {
	value := address.As4()
	return net.IPv4(value[0], value[1], value[2], value[3])
}
