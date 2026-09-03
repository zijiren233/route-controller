// Package planner converts Kubernetes and Cilium state into validated Linux routes.
package planner

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"

	"github.com/zijiren233/route-controller/internal/route"
	corev1 "k8s.io/api/core/v1"
)

type Planner struct {
	podCIDR     netip.Prefix
	serviceCIDR netip.Prefix
	routerCIDR  netip.Prefix
}

func New(podCIDR, serviceCIDR, routerCIDR netip.Prefix) *Planner {
	return &Planner{podCIDR: podCIDR, serviceCIDR: serviceCIDR, routerCIDR: routerCIDR}
}

func (planner *Planner) Workers(
	nodes []corev1.Node,
	pods []corev1.Pod,
	ciliumNodes []CiliumNodeState,
) []Worker {
	nodeByName := make(map[string]*corev1.Node, len(nodes))
	for index := range nodes {
		nodeByName[nodes[index].Name] = &nodes[index]
	}

	agentReady := make(map[string]bool, len(pods))
	for index := range pods {
		pod := &pods[index]
		if pod.DeletionTimestamp == nil && pod.Status.Phase == corev1.PodRunning && podReady(pod) {
			agentReady[pod.Spec.NodeName] = true
		}
	}

	workers := make([]Worker, 0, len(ciliumNodes))
	for _, ciliumNode := range ciliumNodes {
		worker := planner.worker(
			nodeByName[ciliumNode.Name],
			agentReady[ciliumNode.Name],
			ciliumNode,
		)
		workers = append(workers, worker)
	}

	slices.SortFunc(workers, func(first, second Worker) int {
		return cmp.Compare(first.Name, second.Name)
	})

	return workers
}

func (planner *Planner) worker(
	node *corev1.Node,
	agentReady bool,
	ciliumNode CiliumNodeState,
) Worker {
	worker := Worker{Name: ciliumNode.Name}
	state, reason := planner.validateWorker(node, agentReady, ciliumNode, &worker)
	worker.State = state
	worker.Reason = reason
	worker.APIReady = state == WorkerStateReady

	return worker
}

func (planner *Planner) PodRoutes(workers []Worker) ([]route.Desired, error) {
	type allocation struct {
		owner string
		cidr  netip.Prefix
	}

	allocations := make([]allocation, 0)

	desired := make([]route.Desired, 0)
	for _, worker := range workers {
		if !worker.APIReady {
			continue
		}

		for _, podCIDR := range worker.PodCIDRs {
			for _, existing := range allocations {
				if prefixesOverlap(podCIDR, existing.cidr) {
					return nil, fmt.Errorf(
						"PodCIDRs %s on %s and %s on %s overlap",
						podCIDR,
						worker.Name,
						existing.cidr,
						existing.owner,
					)
				}
			}

			allocations = append(allocations, allocation{owner: worker.Name, cidr: podCIDR})

			wanted, err := route.NewDesired(podCIDR, worker.NodeIP)
			if err != nil {
				return nil, fmt.Errorf("create PodCIDR route for worker %s: %w", worker.Name, err)
			}

			desired = append(desired, wanted)
		}
	}

	route.Sort(desired)

	return desired, nil
}

func (planner *Planner) ServiceRoute(workers []Worker) (*route.Desired, error) {
	gateways := make([]netip.Addr, 0, len(workers))
	for _, worker := range workers {
		if worker.APIReady && worker.ProbeReady {
			gateways = append(gateways, worker.NodeIP)
		}
	}

	if len(gateways) == 0 {
		return nil, nil
	}

	wanted, err := route.NewDesired(planner.serviceCIDR, gateways...)
	if err != nil {
		return nil, fmt.Errorf("create Service CIDR route: %w", err)
	}

	return &wanted, nil
}

func (planner *Planner) validateWorker(
	node *corev1.Node,
	agentReady bool,
	ciliumNode CiliumNodeState,
	worker *Worker,
) (WorkerState, string) {
	if node == nil {
		return WorkerStateNodeMissing, "Kubernetes Node is absent"
	}

	if node.DeletionTimestamp != nil {
		return WorkerStateNodeDeleting, "Kubernetes Node is deleting"
	}

	if !nodeReady(node) {
		return WorkerStateNodeNotReady, "Kubernetes Node is not Ready"
	}

	if !agentReady {
		return WorkerStateAgentNotReady, "Cilium agent Pod is not Ready"
	}

	nodeIP, err := netip.ParseAddr(ciliumNode.NodeIP)
	if err != nil || !nodeIP.Is4() || !planner.routerCIDR.Contains(nodeIP) {
		return WorkerStateNodeIPInvalid,
			fmt.Sprintf(
				"CiliumNode InternalIP %q is outside router CIDR %s",
				ciliumNode.NodeIP,
				planner.routerCIDR,
			)
	}

	worker.NodeIP = nodeIP
	if !nodeHasInternalIP(node, nodeIP) {
		return WorkerStateNodeIPMismatch, "CiliumNode InternalIP does not match Node InternalIP"
	}

	if len(ciliumNode.PodCIDRs) == 0 {
		return WorkerStatePodCIDRMissing, "CiliumNode has no PodCIDR"
	}

	worker.PodCIDRs = make([]netip.Prefix, 0, len(ciliumNode.PodCIDRs))
	for _, value := range ciliumNode.PodCIDRs {
		podCIDR, parseErr := netip.ParsePrefix(value)
		if parseErr != nil || !podCIDR.Addr().Is4() {
			return WorkerStatePodCIDRInvalid, fmt.Sprintf("PodCIDR %q is not valid IPv4", value)
		}

		podCIDR = podCIDR.Masked()
		if !route.PrefixWithin(podCIDR, planner.podCIDR) {
			return WorkerStatePodCIDRInvalid,
				fmt.Sprintf("PodCIDR %s is outside allowed CIDR %s", podCIDR, planner.podCIDR)
		}

		if prefixesOverlap(podCIDR, planner.serviceCIDR) {
			return WorkerStatePodCIDRInvalid,
				fmt.Sprintf("PodCIDR %s overlaps Service CIDR %s", podCIDR, planner.serviceCIDR)
		}

		worker.PodCIDRs = append(worker.PodCIDRs, podCIDR)
	}

	healthIP, parseErr := netip.ParseAddr(ciliumNode.HealthIP)
	if parseErr != nil || !healthIP.Is4() || !addressInPrefixes(healthIP, worker.PodCIDRs) {
		return WorkerStateHealthIPInvalid, "Cilium health IP is missing or outside this node's PodCIDRs"
	}

	worker.HealthIP = healthIP

	return WorkerStateReady, ""
}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

func nodeHasInternalIP(node *corev1.Node, expected netip.Addr) bool {
	for _, address := range node.Status.Addresses {
		actual, err := netip.ParseAddr(address.Address)
		if address.Type == corev1.NodeInternalIP && err == nil && actual == expected {
			return true
		}
	}

	return false
}

func addressInPrefixes(address netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}

	return false
}

func prefixesOverlap(first, second netip.Prefix) bool {
	return first.Contains(second.Addr()) || second.Contains(first.Addr())
}
