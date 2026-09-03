// Package route defines desired routes and the backend ownership contract.
package route

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

type ActionType string

const (
	ActionReplace ActionType = "replace"
	ActionDelete  ActionType = "delete"
)

type Desired struct {
	Destination netip.Prefix `json:"destination"`
	Gateways    []netip.Addr `json:"gateways"`
}

type Action struct {
	Type        ActionType   `json:"type"`
	Destination netip.Prefix `json:"destination"`
}

func (action Action) String() string {
	return string(action.Type) + " " + action.Destination.String()
}

type Result struct {
	Actions []Action `json:"actions,omitempty"`
}

type ReconcileOptions struct {
	Prune  bool
	DryRun bool
}

type Backend interface {
	Reconcile(ctx context.Context, desired []Desired, options ReconcileOptions) (Result, error)
}

func NewDesired(destination netip.Prefix, gateways ...netip.Addr) (Desired, error) {
	destination = destination.Masked()
	if !destination.IsValid() || !destination.Addr().Is4() {
		return Desired{}, fmt.Errorf("invalid IPv4 destination %q", destination)
	}

	unique := make(map[netip.Addr]struct{}, len(gateways))
	for _, gateway := range gateways {
		gateway = gateway.Unmap()
		if !gateway.IsValid() || !gateway.Is4() {
			return Desired{}, fmt.Errorf("invalid IPv4 gateway %q", gateway)
		}

		unique[gateway] = struct{}{}
	}

	if len(unique) == 0 {
		return Desired{}, fmt.Errorf("route %s has no gateway", destination)
	}

	normalized := make([]netip.Addr, 0, len(unique))
	for gateway := range unique {
		normalized = append(normalized, gateway)
	}

	slices.SortFunc(normalized, netip.Addr.Compare)

	return Desired{Destination: destination, Gateways: normalized}, nil
}

func Sort(routes []Desired) {
	slices.SortFunc(routes, func(first, second Desired) int {
		return cmp.Compare(first.Destination.String(), second.Destination.String())
	})
}

func (desired Desired) String() string {
	gateways := make([]string, 0, len(desired.Gateways))
	for _, gateway := range desired.Gateways {
		gateways = append(gateways, gateway.String())
	}

	return fmt.Sprintf("%s via [%s]", desired.Destination, strings.Join(gateways, ","))
}

func PrefixWithin(child, parent netip.Prefix) bool {
	child = child.Masked()
	parent = parent.Masked()
	return child.IsValid() && parent.IsValid() && child.Addr().BitLen() == parent.Addr().BitLen() &&
		child.Bits() >= parent.Bits() && parent.Contains(child.Addr())
}
