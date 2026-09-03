package planner

import "net/netip"

type WorkerState string

const (
	WorkerStateReady           WorkerState = "ready"
	WorkerStateNodeMissing     WorkerState = "node-missing"
	WorkerStateNodeDeleting    WorkerState = "node-deleting"
	WorkerStateNodeNotReady    WorkerState = "node-not-ready"
	WorkerStateAgentNotReady   WorkerState = "cilium-agent-not-ready"
	WorkerStateNodeIPInvalid   WorkerState = "node-ip-invalid"
	WorkerStateNodeIPMismatch  WorkerState = "node-ip-mismatch"
	WorkerStatePodCIDRMissing  WorkerState = "pod-cidr-missing"
	WorkerStatePodCIDRInvalid  WorkerState = "pod-cidr-invalid"
	WorkerStateHealthIPInvalid WorkerState = "health-ip-invalid"
)

type CiliumNodeState struct {
	Name     string
	NodeIP   string
	HealthIP string
	PodCIDRs []string
}

type Worker struct {
	Name       string         `json:"name"`
	NodeIP     netip.Addr     `json:"nodeIP,omitempty"`
	HealthIP   netip.Addr     `json:"healthIP,omitempty"`
	PodCIDRs   []netip.Prefix `json:"podCIDRs,omitempty"`
	State      WorkerState    `json:"state"`
	Reason     string         `json:"reason,omitempty"`
	APIReady   bool           `json:"apiReady"`
	ProbeReady bool           `json:"probeReady"`
}
