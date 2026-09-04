package discovery_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zijiren233/route-controller/internal/config"
	"github.com/zijiren233/route-controller/internal/discovery"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveDiscoversRouteConfiguration(t *testing.T) {
	t.Parallel()

	resolver := discovery.NewResolver(
		fakeReader(t, discoveryObjects()...),
		&fakeNetwork{
			interfaces: map[netip.Addr]string{
				netip.MustParseAddr("192.0.2.10"): "ens18",
				netip.MustParseAddr("192.0.2.11"): "ens18",
			},
			prefixes: []netip.Prefix{
				netip.MustParsePrefix("192.0.0.0/16"),
				netip.MustParsePrefix("192.0.2.0/24"),
			},
		},
	)

	resolved, err := resolver.Resolve(context.Background(), config.Defaults())

	require.NoError(t, err)
	assert.Equal(t, "ens18", resolved.Routes.Interface)
	assert.Equal(t, "10.0.0.0/10", resolved.PodCIDR.String())
	assert.Equal(t, "10.192.0.0/12", resolved.ServiceCIDR.String())
	assert.Equal(t, "192.0.2.0/24", resolved.RouterCIDR.String())
}

func TestResolveKeepsExplicitNetworkConfiguration(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	cfg.Routes.Interface = "eth0"
	cfg.Routes.PodCIDR = "10.0.0.0/10"
	cfg.Routes.ServiceCIDR = "10.192.0.0/12"
	cfg.Routes.RouterCIDR = "192.0.2.0/24"
	network := &fakeNetwork{}
	resolver := discovery.NewResolver(fakeReader(t), network)

	resolved, err := resolver.Resolve(context.Background(), cfg)

	require.NoError(t, err)
	assert.Equal(t, "eth0", resolved.Routes.Interface)
	assert.Zero(t, network.interfaceCalls)
	assert.Zero(t, network.prefixCalls)
}

func TestResolveFallsBackToKubeadmPodCIDR(t *testing.T) {
	t.Parallel()

	objects := discoveryObjects()[1:]
	resolver := discovery.NewResolver(
		fakeReader(t, objects...),
		&fakeNetwork{
			interfaces: map[netip.Addr]string{
				netip.MustParseAddr("192.0.2.10"): "ens18",
				netip.MustParseAddr("192.0.2.11"): "ens18",
			},
			prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
		},
	)

	resolved, err := resolver.Resolve(context.Background(), config.Defaults())

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0/10", resolved.PodCIDR.String())
}

func TestResolveRejectsWorkersOnDifferentInterfaces(t *testing.T) {
	t.Parallel()

	resolver := discovery.NewResolver(
		fakeReader(t, discoveryObjects()...),
		&fakeNetwork{interfaces: map[netip.Addr]string{
			netip.MustParseAddr("192.0.2.10"): "eth0",
			netip.MustParseAddr("192.0.2.11"): "eth1",
		}},
	)

	_, err := resolver.Resolve(context.Background(), config.Defaults())

	require.Error(t, err)
	assert.ErrorContains(t, err, "uses interface")
}

func TestResolveRejectsRouterCIDROutsideWorkerNetwork(t *testing.T) {
	t.Parallel()

	network := &fakeNetwork{
		interfaces: map[netip.Addr]string{
			netip.MustParseAddr("192.0.2.10"): "eth0",
			netip.MustParseAddr("192.0.2.11"): "eth0",
		},
		prefixes: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
	}
	resolver := discovery.NewResolver(fakeReader(t, discoveryObjects()...), network)

	_, err := resolver.Resolve(context.Background(), config.Defaults())

	require.Error(t, err)
	assert.ErrorContains(t, err, "no connected IPv4 CIDR")
}

type fakeNetwork struct {
	interfaces     map[netip.Addr]string
	prefixes       []netip.Prefix
	interfaceCalls int
	prefixCalls    int
}

func (network *fakeNetwork) InterfaceFor(address netip.Addr) (string, error) {
	network.interfaceCalls++
	return network.interfaces[address], nil
}

func (network *fakeNetwork) ConnectedPrefixes(string) ([]netip.Prefix, error) {
	network.prefixCalls++
	return network.prefixes, nil
}

func fakeReader(t *testing.T, objects ...client.Object) client.Reader {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func discoveryObjects() []client.Object {
	return []client.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cilium-config", Namespace: metav1.NamespaceSystem},
			Data:       map[string]string{"cluster-pool-ipv4-cidr": "10.0.0.0/10"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kubeadm-config",
				Namespace: metav1.NamespaceSystem,
			},
			Data: map[string]string{"ClusterConfiguration": `
networking:
  podSubnet: 10.0.0.0/10
  serviceSubnet: 10.192.0.0/12
`},
		},
		workerNode("worker-1", "192.0.2.10", nil),
		workerNode("worker-2", "192.0.2.11", nil),
		workerNode(
			"control-plane-1",
			"192.0.2.2",
			map[string]string{"node-role.kubernetes.io/control-plane": ""},
		),
	}
}

func workerNode(name, address string, labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{
			Type: corev1.NodeInternalIP, Address: address,
		}}},
	}
}
