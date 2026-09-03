package route_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zijiren233/route-controller/internal/route"
)

func TestNewDesiredNormalizesGateways(t *testing.T) {
	t.Parallel()

	desired, err := route.NewDesired(
		netip.MustParsePrefix("10.192.1.1/12"),
		netip.MustParseAddr("192.0.2.191"),
		netip.MustParseAddr("192.0.2.190"),
		netip.MustParseAddr("192.0.2.190"),
	)
	require.NoError(t, err)
	assert.Equal(t, netip.MustParsePrefix("10.192.0.0/12"), desired.Destination)
	assert.Equal(t, []netip.Addr{
		netip.MustParseAddr("192.0.2.190"),
		netip.MustParseAddr("192.0.2.191"),
	}, desired.Gateways)
}

func TestNewDesiredRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	_, err := route.NewDesired(
		netip.MustParsePrefix("2001:db8::/64"),
		netip.MustParseAddr("192.0.2.1"),
	)
	assert.Error(t, err)
	_, err = route.NewDesired(netip.MustParsePrefix("10.0.0.0/24"))
	assert.Error(t, err)
}

func TestPrefixWithin(t *testing.T) {
	t.Parallel()

	assert.True(t, route.PrefixWithin(
		netip.MustParsePrefix("10.0.3.0/24"),
		netip.MustParsePrefix("10.0.0.0/10"),
	))
	assert.False(t, route.PrefixWithin(
		netip.MustParsePrefix("10.192.0.0/12"),
		netip.MustParsePrefix("10.0.0.0/10"),
	))
}
