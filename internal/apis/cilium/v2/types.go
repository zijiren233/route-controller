// Package v2 contains the CiliumNode fields used by the controller.
package v2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	Group   = "cilium.io"
	Version = "v2"
)

var GroupVersion = schema.GroupVersion{Group: Group, Version: Version}

// CiliumNode models the stable Cilium v2 fields required for native route discovery.
type CiliumNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CiliumNodeSpec `json:"spec,omitempty"`
}

type CiliumNodeSpec struct {
	Addresses []NodeAddress `json:"addresses,omitempty"`
	Health    HealthAddress `json:"health,omitempty"`
	IPAM      IPAMSpec      `json:"ipam,omitempty"`
}

type NodeAddress struct {
	Type string `json:"type,omitempty"`
	IP   string `json:"ip,omitempty"`
}

type HealthAddress struct {
	IPv4 string `json:"ipv4,omitempty"`
}

type IPAMSpec struct {
	PodCIDRs []string `json:"podCIDRs,omitempty"`
}

// CiliumNodeList is the list form registered with the Kubernetes scheme.
type CiliumNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CiliumNode `json:"items"`
}

func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &CiliumNode{}, &CiliumNodeList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

func (in *CiliumNode) DeepCopyInto(out *CiliumNode) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec.Addresses = append([]NodeAddress(nil), in.Spec.Addresses...)
	out.Spec.IPAM.PodCIDRs = append([]string(nil), in.Spec.IPAM.PodCIDRs...)
}

func (in *CiliumNode) DeepCopy() *CiliumNode {
	if in == nil {
		return nil
	}

	out := new(CiliumNode)
	in.DeepCopyInto(out)

	return out
}

func (in *CiliumNode) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *CiliumNodeList) DeepCopyInto(out *CiliumNodeList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)

	if in.Items != nil {
		out.Items = make([]CiliumNode, len(in.Items))
		for index := range in.Items {
			in.Items[index].DeepCopyInto(&out.Items[index])
		}
	}
}

func (in *CiliumNodeList) DeepCopy() *CiliumNodeList {
	if in == nil {
		return nil
	}

	out := new(CiliumNodeList)
	in.DeepCopyInto(out)

	return out
}

func (in *CiliumNodeList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
