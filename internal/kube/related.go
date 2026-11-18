package kube

import (
	"context"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Links between objects that the details drawer shows: the pods on a node, and the
// PersistentVolumeClaim ↔ PersistentVolume ↔ pods chain of a volume.

var (
	podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	pvcGVR  = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	pvGVR   = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}

	podKind = Kind{Name: "pods", Version: "v1", Kind: "Pod", Namespaced: true}
	pvcKind = Kind{Name: "persistentvolumeclaims", Version: "v1", Kind: "PersistentVolumeClaim", Namespaced: true}
	pvKind  = Kind{Name: "persistentvolumes", Version: "v1", Kind: "PersistentVolume"}
)

// PodsOnNode lists the pods scheduled on one node, in every namespace, with live
// CPU / memory like the pod list. The field selector keeps the answer small on big
// clusters; the name check below makes it exact even if a server ignores it.
func (c *Cluster) PodsOnNode(ctx context.Context, node string) ([]Row, error) {
	var usage map[string]Usage
	usageDone := make(chan struct{})
	go func() { defer close(usageDone); usage, _ = c.PodUsage(ctx, "") }()
	list, err := c.Dynamic.Resource(podsGVR).List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node})
	<-usageDone
	if err != nil {
		return nil, err
	}
	var items []unstructured.Unstructured
	for _, p := range list.Items {
		if str(p.Object, "spec", "nodeName") == node {
			items = append(items, p)
		}
	}
	return podRows(items, usage), nil
}

// VolumeLinks is what a PVC or a PV is connected to. Claim and Volume are nil when
// the other side does not exist (a Pending claim, a Released volume, a deleted PVC);
// VolumeName / ClaimName still say what the object points at.
type VolumeLinks struct {
	Claim      *Row   `json:"claim,omitempty"`
	ClaimName  string `json:"claimName,omitempty"`
	ClaimNS    string `json:"claimNamespace,omitempty"`
	Volume     *Row   `json:"volume,omitempty"`
	VolumeName string `json:"volumeName,omitempty"`
	Pods       []Row  `json:"pods"`
}

// ClaimLinks returns a PVC's volume and the pods that mount it.
func (c *Cluster) ClaimLinks(ctx context.Context, namespace, name string) (VolumeLinks, error) {
	pvc, err := c.Dynamic.Resource(pvcGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return VolumeLinks{}, err
	}
	r := toRow(pvcKind, pvc)
	out := VolumeLinks{Claim: &r, ClaimName: name, ClaimNS: namespace, VolumeName: str(pvc.Object, "spec", "volumeName")}
	if out.VolumeName != "" {
		if pv, err := c.Dynamic.Resource(pvGVR).Get(ctx, out.VolumeName, metav1.GetOptions{}); err == nil {
			vr := toRow(pvKind, pv)
			out.Volume = &vr
		}
	}
	out.Pods, err = c.podsUsingClaim(ctx, namespace, name)
	return out, err
}

// VolumeClaimLinks returns a PV's claim (from spec.claimRef) and the pods using it.
func (c *Cluster) VolumeClaimLinks(ctx context.Context, name string) (VolumeLinks, error) {
	pv, err := c.Dynamic.Resource(pvGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return VolumeLinks{}, err
	}
	vr := toRow(pvKind, pv)
	out := VolumeLinks{Volume: &vr, VolumeName: name,
		ClaimName: str(pv.Object, "spec", "claimRef", "name"), ClaimNS: str(pv.Object, "spec", "claimRef", "namespace")}
	out.Pods = []Row{}
	if out.ClaimName == "" {
		return out, nil
	}
	if pvc, err := c.Dynamic.Resource(pvcGVR).Namespace(out.ClaimNS).Get(ctx, out.ClaimName, metav1.GetOptions{}); err == nil {
		// claimRef carries the claim's uid: a PVC re-created with the same name is a
		// different claim, and this volume is not bound to it.
		if uid := str(pv.Object, "spec", "claimRef", "uid"); uid == "" || uid == string(pvc.GetUID()) {
			r := toRow(pvcKind, pvc)
			out.Claim = &r
		}
	}
	if out.Claim == nil {
		return out, nil
	}
	out.Pods, err = c.podsUsingClaim(ctx, out.ClaimNS, out.ClaimName)
	return out, err
}

func (c *Cluster) podsUsingClaim(ctx context.Context, namespace, claim string) ([]Row, error) {
	var usage map[string]Usage
	usageDone := make(chan struct{})
	go func() { defer close(usageDone); usage, _ = c.PodUsage(ctx, namespace) }()
	list, err := c.Dynamic.Resource(podsGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	<-usageDone
	if err != nil {
		return nil, err
	}
	var items []unstructured.Unstructured
	for _, p := range list.Items {
		if mountsClaim(p.Object, p.GetName(), claim) {
			items = append(items, p)
		}
	}
	return podRows(items, usage), nil
}

// mountsClaim reports whether a pod uses the claim: directly
// (volumes[].persistentVolumeClaim.claimName, also how StatefulSet templates end up)
// or as a generic ephemeral volume, whose PVC is named <pod>-<volume>.
func mountsClaim(pod map[string]any, podName, claim string) bool {
	vols, _, _ := unstructured.NestedSlice(pod, "spec", "volumes")
	for _, v := range vols {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if str(vm, "persistentVolumeClaim", "claimName") == claim {
			return true
		}
		if _, eph := vm["ephemeral"]; eph && podName+"-"+str(vm, "name") == claim {
			return true
		}
	}
	return false
}

func podRows(items []unstructured.Unstructured, usage map[string]Usage) []Row {
	rows := make([]Row, 0, len(items))
	objs := make(map[string]map[string]any, len(items))
	for i := range items {
		rows = append(rows, toRow(podKind, &items[i]))
		objs[items[i].GetNamespace()+"/"+items[i].GetName()] = items[i].Object
	}
	addUsage(rows, objs, usage)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Namespace != rows[j].Namespace {
			return rows[i].Namespace < rows[j].Namespace
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}
