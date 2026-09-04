// Package discovery resolves route configuration from Kubernetes and the local network.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/zijiren233/route-controller/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	ciliumConfigMapName  = "cilium-config"
	kubeadmConfigMapName = "kubeadm-config"
	ciliumPodCIDRKey     = "cluster-pool-ipv4-cidr"
)

type Network interface {
	InterfaceFor(address netip.Addr) (string, error)
	ConnectedPrefixes(interfaceName string) ([]netip.Prefix, error)
}

type Resolver struct {
	reader  client.Reader
	network Network
}

type clusterNetworking struct {
	PodSubnet     string `yaml:"podSubnet"`
	ServiceSubnet string `yaml:"serviceSubnet"`
}

func NewResolver(reader client.Reader, network Network) *Resolver {
	return &Resolver{reader: reader, network: network}
}

func (resolver *Resolver) Resolve(
	ctx context.Context,
	cfg config.Config,
) (config.Validated, error) {
	var err error
	if cfg.Routes.PodCIDR == "" {
		cfg.Routes.PodCIDR, err = resolver.podCIDR(ctx)
		if err != nil {
			return config.Validated{}, err
		}
	}

	if cfg.Routes.ServiceCIDR == "" {
		cfg.Routes.ServiceCIDR, err = resolver.serviceCIDR(ctx)
		if err != nil {
			return config.Validated{}, err
		}
	}

	if cfg.Routes.Interface == "" || cfg.Routes.RouterCIDR == "" {
		workerIPs, workerErr := resolver.workerIPs(ctx)
		if workerErr != nil {
			return config.Validated{}, workerErr
		}

		cfg.Routes.Interface, err = resolver.routeInterface(cfg.Routes.Interface, workerIPs)
		if err != nil {
			return config.Validated{}, err
		}

		cfg.Routes.RouterCIDR, err = resolver.routerCIDR(
			cfg.Routes.RouterCIDR,
			cfg.Routes.Interface,
			workerIPs,
		)
		if err != nil {
			return config.Validated{}, err
		}
	}

	validated, err := cfg.Validate()
	if err != nil {
		return config.Validated{}, fmt.Errorf("validate resolved configuration: %w", err)
	}

	return validated, nil
}

func (resolver *Resolver) podCIDR(ctx context.Context) (string, error) {
	configMap := new(corev1.ConfigMap)

	ciliumErr := resolver.reader.Get(
		ctx,
		client.ObjectKey{Namespace: metav1.NamespaceSystem, Name: ciliumConfigMapName},
		configMap,
	)
	if ciliumErr == nil {
		var prefix netip.Prefix

		prefix, ciliumErr = singleIPv4Prefix(configMap.Data[ciliumPodCIDRKey])
		if ciliumErr == nil {
			return prefix.String(), nil
		}
	}

	networking, kubeadmErr := resolver.kubeadmNetworking(ctx)

	var prefix netip.Prefix
	if kubeadmErr == nil {
		prefix, kubeadmErr = singleIPv4Prefix(networking.PodSubnet)
	}

	if kubeadmErr != nil {
		return "", fmt.Errorf(
			"discover Pod CIDR from Cilium and kubeadm configuration: %w; set --pod-cidr explicitly",
			errors.Join(ciliumErr, kubeadmErr),
		)
	}

	return prefix.String(), nil
}

func (resolver *Resolver) serviceCIDR(ctx context.Context) (string, error) {
	networking, err := resolver.kubeadmNetworking(ctx)
	if err != nil {
		return "", err
	}

	prefix, err := singleIPv4Prefix(networking.ServiceSubnet)
	if err != nil {
		return "", fmt.Errorf(
			"discover Service CIDR from ConfigMap %s/%s: %w; set --service-cidr explicitly",
			metav1.NamespaceSystem,
			kubeadmConfigMapName,
			err,
		)
	}

	return prefix.String(), nil
}

func (resolver *Resolver) kubeadmNetworking(ctx context.Context) (clusterNetworking, error) {
	configMap := new(corev1.ConfigMap)
	if err := resolver.reader.Get(
		ctx,
		client.ObjectKey{Namespace: metav1.NamespaceSystem, Name: kubeadmConfigMapName},
		configMap,
	); err != nil {
		return clusterNetworking{}, fmt.Errorf("get kubeadm configuration: %w", err)
	}

	clusterConfiguration := struct {
		Networking clusterNetworking `yaml:"networking"`
	}{}
	if err := yaml.Unmarshal(
		[]byte(configMap.Data["ClusterConfiguration"]),
		&clusterConfiguration,
	); err != nil {
		return clusterNetworking{}, fmt.Errorf("decode kubeadm ClusterConfiguration: %w", err)
	}

	return clusterConfiguration.Networking, nil
}

func (resolver *Resolver) workerIPs(ctx context.Context) ([]netip.Addr, error) {
	nodes := new(corev1.NodeList)
	if err := resolver.reader.List(ctx, nodes); err != nil {
		return nil, fmt.Errorf("list Nodes for route discovery: %w", err)
	}

	addresses := make([]netip.Addr, 0, len(nodes.Items))
	for index := range nodes.Items {
		node := &nodes.Items[index]
		if isControlPlane(node) {
			continue
		}

		address, err := nodeIPv4InternalAddress(node)
		if err != nil {
			return nil, err
		}

		addresses = append(addresses, address)
	}

	if len(addresses) == 0 {
		return nil, errors.New("discover worker routes: no worker Nodes are registered")
	}

	slices.SortFunc(addresses, netip.Addr.Compare)

	return slices.Compact(addresses), nil
}

func (resolver *Resolver) routeInterface(
	configured string,
	workerIPs []netip.Addr,
) (string, error) {
	selected := configured
	for _, workerIP := range workerIPs {
		name, err := resolver.network.InterfaceFor(workerIP)
		if err != nil {
			return "", fmt.Errorf("discover interface for worker %s: %w", workerIP, err)
		}

		if selected == "" {
			selected = name
		}

		if name != selected {
			return "", fmt.Errorf(
				"worker %s uses interface %q, expected %q; set --interface only when all workers share it",
				workerIP,
				name,
				selected,
			)
		}
	}

	return selected, nil
}

func (resolver *Resolver) routerCIDR(
	configured string,
	interfaceName string,
	workerIPs []netip.Addr,
) (string, error) {
	if configured != "" {
		prefix, err := netip.ParsePrefix(configured)
		if err != nil || !prefix.Addr().Is4() {
			return "", fmt.Errorf("configured router CIDR %q is not valid IPv4", configured)
		}

		prefix = prefix.Masked()
		if err := validateContainsWorkers(prefix, workerIPs); err != nil {
			return "", err
		}

		return prefix.String(), nil
	}

	prefixes, err := resolver.network.ConnectedPrefixes(interfaceName)
	if err != nil {
		return "", fmt.Errorf("list connected routes on interface %q: %w", interfaceName, err)
	}

	matching := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		prefix = prefix.Masked()
		if prefix.Addr().Is4() && validateContainsWorkers(prefix, workerIPs) == nil {
			matching = append(matching, prefix)
		}
	}

	if len(matching) == 0 {
		return "", fmt.Errorf(
			"no connected IPv4 CIDR on interface %q contains every worker; set --router-cidr explicitly",
			interfaceName,
		)
	}

	slices.SortFunc(matching, func(first, second netip.Prefix) int {
		return second.Bits() - first.Bits()
	})

	mostSpecific := matching[0]
	for _, prefix := range matching[1:] {
		if prefix.Bits() == mostSpecific.Bits() && prefix != mostSpecific {
			return "", fmt.Errorf(
				"multiple connected CIDRs on interface %q match every worker; set --router-cidr explicitly",
				interfaceName,
			)
		}
	}

	return mostSpecific.String(), nil
}

func singleIPv4Prefix(value string) (netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, 1)
	for part := range strings.SplitSeq(value, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err == nil && prefix.Addr().Is4() {
			prefixes = append(prefixes, prefix.Masked())
		}
	}

	if len(prefixes) != 1 {
		return netip.Prefix{}, fmt.Errorf("expected exactly one IPv4 CIDR, found %d", len(prefixes))
	}

	return prefixes[0], nil
}

func isControlPlane(node *corev1.Node) bool {
	_, controlPlane := node.Labels["node-role.kubernetes.io/control-plane"]
	_, legacyControlPlane := node.Labels["node-role.kubernetes.io/master"]
	return controlPlane || legacyControlPlane
}

func nodeIPv4InternalAddress(node *corev1.Node) (netip.Addr, error) {
	addresses := make([]netip.Addr, 0, 1)
	for _, nodeAddress := range node.Status.Addresses {
		address, err := netip.ParseAddr(nodeAddress.Address)
		if nodeAddress.Type == corev1.NodeInternalIP && err == nil && address.Is4() {
			addresses = append(addresses, address)
		}
	}

	if len(addresses) != 1 {
		return netip.Addr{}, fmt.Errorf(
			"worker Node %q must have exactly one IPv4 InternalIP, found %d",
			node.Name,
			len(addresses),
		)
	}

	return addresses[0], nil
}

func validateContainsWorkers(prefix netip.Prefix, workerIPs []netip.Addr) error {
	for _, workerIP := range workerIPs {
		if !prefix.Contains(workerIP) {
			return fmt.Errorf("router CIDR %s does not contain worker %s", prefix, workerIP)
		}
	}

	return nil
}
