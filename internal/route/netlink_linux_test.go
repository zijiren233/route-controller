//go:build linux

package route

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
)

func TestNetlinkBackendReconcile(t *testing.T) {
	t.Parallel()

	api := &fakeNetlink{link: &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "test0", Index: 7}}}
	backend, err := newNetlinkBackend(
		api,
		"test0",
		254,
		99,
		netip.MustParsePrefix("10.0.0.0/10"),
		netip.MustParsePrefix("10.192.0.0/12"),
	)
	require.NoError(t, err)
	desired := []Desired{
		mustDesired(t, "10.0.3.0/24", "192.0.2.190"),
		mustDesired(t, "10.192.0.0/12", "192.0.2.190", "192.0.2.191"),
	}

	result, err := backend.Reconcile(context.Background(), desired, ReconcileOptions{Prune: true})
	require.NoError(t, err)
	assert.Len(t, result.Actions, 2)
	assert.Len(t, api.routes, 2)

	result, err = backend.Reconcile(context.Background(), desired, ReconcileOptions{Prune: true})
	require.NoError(t, err)
	assert.Empty(t, result.Actions, "an identical desired state must be idempotent")
}

func TestNetlinkBackendRejectsForeignProtocolBeforeMutation(t *testing.T) {
	t.Parallel()

	destination := netip.MustParsePrefix("10.0.3.0/24")
	api := &fakeNetlink{
		link: &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "test0", Index: 7}},
		routes: []netlink.Route{{
			Dst: prefixToIPNet(destination), Table: 254, Protocol: 100,
		}},
	}
	backend, err := newNetlinkBackend(
		api,
		"test0",
		254,
		99,
		netip.MustParsePrefix("10.0.0.0/10"),
		netip.MustParsePrefix("10.192.0.0/12"),
	)
	require.NoError(t, err)

	_, err = backend.Reconcile(
		context.Background(),
		[]Desired{mustDesired(t, destination.String(), "192.0.2.190")},
		ReconcileOptions{},
	)
	assert.ErrorContains(t, err, "route conflict")
	assert.Zero(t, api.replaceCalls)
}

func TestNetlinkBackendRepairsOwnedRouteMetadataDrift(t *testing.T) {
	t.Parallel()

	desired := mustDesired(t, "10.0.3.0/24", "192.0.2.190")
	drifted := buildRoute(desired, 7, 254, 99)
	drifted.Scope = netlink.SCOPE_LINK
	api := &fakeNetlink{
		link:   &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "test0", Index: 7}},
		routes: []netlink.Route{*drifted},
	}
	backend, err := newNetlinkBackend(
		api,
		"test0",
		254,
		99,
		netip.MustParsePrefix("10.0.0.0/10"),
		netip.MustParsePrefix("10.192.0.0/12"),
	)
	require.NoError(t, err)

	result, err := backend.Reconcile(
		context.Background(),
		[]Desired{desired},
		ReconcileOptions{},
	)
	require.NoError(t, err)
	require.Len(t, result.Actions, 1)
	assert.Equal(t, ActionReplace, result.Actions[0].Type)
	assert.Equal(t, netlink.SCOPE_UNIVERSE, api.routes[0].Scope)
}

func TestNetlinkBackendPrunesOnlyOwnedManagedRoutes(t *testing.T) {
	t.Parallel()

	ownedPod := buildRoute(
		mustDesired(t, "10.0.3.0/24", "192.0.2.190"),
		7,
		254,
		99,
	)
	ownedOutside := buildRoute(
		mustDesired(t, "172.16.0.0/16", "192.0.2.190"),
		7,
		254,
		99,
	)
	foreign := buildRoute(
		mustDesired(t, "10.0.4.0/24", "192.0.2.191"),
		7,
		254,
		100,
	)
	api := &fakeNetlink{
		link:   &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "test0", Index: 7}},
		routes: []netlink.Route{*ownedPod, *ownedOutside, *foreign},
	}
	backend, err := newNetlinkBackend(
		api,
		"test0",
		254,
		99,
		netip.MustParsePrefix("10.0.0.0/10"),
		netip.MustParsePrefix("10.192.0.0/12"),
	)
	require.NoError(t, err)

	result, err := backend.Reconcile(context.Background(), nil, ReconcileOptions{Prune: true})
	require.NoError(t, err)
	require.Len(t, result.Actions, 1)
	assert.Equal(t, ActionDelete, result.Actions[0].Type)
	assert.Len(t, api.routes, 2)
}

type fakeNetlink struct {
	link         netlink.Link
	routes       []netlink.Route
	replaceCalls int
}

func (api *fakeNetlink) LinkByName(string) (netlink.Link, error) {
	return api.link, nil
}

func (api *fakeNetlink) RouteListFiltered(int, *netlink.Route, uint64) ([]netlink.Route, error) {
	return append([]netlink.Route(nil), api.routes...), nil
}

func (api *fakeNetlink) RouteReplace(replacement *netlink.Route) error {
	api.replaceCalls++

	wantedDestination, _ := routeDestination(replacement)
	for index := range api.routes {
		destination, ok := routeDestination(&api.routes[index])
		if ok && destination == wantedDestination &&
			api.routes[index].Protocol == replacement.Protocol {
			api.routes[index] = *replacement
			return nil
		}
	}

	api.routes = append(api.routes, *replacement)

	return nil
}

func (api *fakeNetlink) RouteDel(deleted *netlink.Route) error {
	for index := range api.routes {
		if &api.routes[index] == deleted || api.routes[index].String() == deleted.String() {
			api.routes = append(api.routes[:index], api.routes[index+1:]...)
			return nil
		}
	}

	return nil
}

func mustDesired(t *testing.T, destination string, gateways ...string) Desired {
	t.Helper()

	addresses := make([]netip.Addr, 0, len(gateways))
	for _, gateway := range gateways {
		addresses = append(addresses, netip.MustParseAddr(gateway))
	}

	desired, err := NewDesired(netip.MustParsePrefix(destination), addresses...)
	require.NoError(t, err)

	return desired
}
