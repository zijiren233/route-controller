//go:build integration && linux

package integration_test

import (
	"context"
	"net"
	"net/netip"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"github.com/zijiren233/route-controller/internal/route"
)

const (
	integrationLinkName = "route-ctl-test"
	integrationTable    = 39_099
	integrationProtocol = 99
)

func TestNetlinkLifecycle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("netlink integration test requires root")
	}

	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: integrationLinkName}}
	require.NoError(t, netlink.LinkAdd(link))
	t.Cleanup(func() {
		_ = netlink.LinkDel(link)
	})
	require.NoError(t, netlink.LinkSetUp(link))
	require.NoError(t, netlink.AddrAdd(link, &netlink.Addr{
		IPNet: &net.IPNet{IP: net.ParseIP("192.0.2.1"), Mask: net.CIDRMask(24, 32)},
	}))

	backend, err := route.NewNetlinkBackend(
		integrationLinkName,
		integrationTable,
		integrationProtocol,
		netip.MustParsePrefix("10.240.0.0/16"),
		netip.MustParsePrefix("10.241.0.0/16"),
	)
	require.NoError(t, err)
	podRoute := desiredRoute(t, "10.240.1.0/24", "192.0.2.2")
	serviceRoute := desiredRoute(t, "10.241.0.0/16", "192.0.2.2", "192.0.2.3")

	result, err := backend.Reconcile(
		context.Background(),
		[]route.Desired{podRoute, serviceRoute},
		route.ReconcileOptions{Prune: true},
	)
	require.NoError(t, err)
	assert.Len(t, result.Actions, 2)
	assert.Len(t, routesInIntegrationTable(t), 2)

	serviceRoute = desiredRoute(t, "10.241.0.0/16", "192.0.2.3")
	_, err = backend.Reconcile(
		context.Background(),
		[]route.Desired{serviceRoute},
		route.ReconcileOptions{Prune: true},
	)
	require.NoError(t, err)
	routes := routesInIntegrationTable(t)
	require.Len(t, routes, 1)
	assert.Equal(t, "10.241.0.0/16", routes[0].Dst.String())
	assert.Equal(t, "192.0.2.3", routes[0].Gw.String())

	foreignDestination := netip.MustParsePrefix("10.240.2.0/24")
	foreign := netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst: &net.IPNet{
			IP:   net.ParseIP(foreignDestination.Addr().String()),
			Mask: net.CIDRMask(foreignDestination.Bits(), 32),
		},
		Gw:       net.ParseIP("192.0.2.2"),
		Table:    integrationTable,
		Protocol: 100,
	}
	require.NoError(t, netlink.RouteReplace(&foreign))
	_, err = backend.Reconcile(
		context.Background(),
		[]route.Desired{desiredRoute(t, foreignDestination.String(), "192.0.2.3")},
		route.ReconcileOptions{},
	)
	assert.ErrorContains(t, err, "route conflict")
}

func routesInIntegrationTable(t *testing.T) []netlink.Route {
	t.Helper()

	routes, err := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: integrationTable},
		netlink.RT_FILTER_TABLE,
	)
	require.NoError(t, err)

	return routes
}

func desiredRoute(t *testing.T, destination string, gateways ...string) route.Desired {
	t.Helper()

	addresses := make([]netip.Addr, 0, len(gateways))
	for _, gateway := range gateways {
		addresses = append(addresses, netip.MustParseAddr(gateway))
	}

	desired, err := route.NewDesired(netip.MustParsePrefix(destination), addresses...)
	require.NoError(t, err, "build route "+destination)

	return desired
}
