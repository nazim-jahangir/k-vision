package kube

import (
	"context"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestPoolOf(t *testing.T) {
	cases := []struct {
		labels              map[string]string
		pool, src, selector string
		spot                bool
	}{
		{map[string]string{"karpenter.sh/nodepool": "default", "karpenter.sh/capacity-type": "spot"}, "default", "karpenter", "karpenter.sh/nodepool=default", true},
		{map[string]string{"eks.amazonaws.com/nodegroup": "ng-1"}, "ng-1", "eks", "eks.amazonaws.com/nodegroup=ng-1", false},
		{map[string]string{"alpha.eksctl.io/nodegroup-name": "ng-2", "eks.amazonaws.com/capacityType": "SPOT"}, "ng-2", "eks", "alpha.eksctl.io/nodegroup-name=ng-2", true},
		{map[string]string{"eks.amazonaws.com/compute-type": "fargate"}, "fargate", "eks", "eks.amazonaws.com/compute-type=fargate", false},
		{map[string]string{"cloud.google.com/gke-nodepool": "general-pool-a1b2"}, "general-pool-a1b2", "gke", "cloud.google.com/gke-nodepool=general-pool-a1b2", false},
		{map[string]string{"cloud.google.com/gke-nodepool": "nap-n2-standard-8-spot-x1y2z3", "cloud.google.com/gke-spot": "true"},
			"nap-n2-standard-8-spot-x1y2z3", "gke-nap", "cloud.google.com/gke-nodepool=nap-n2-standard-8-spot-x1y2z3", true},
		{map[string]string{"cloud.google.com/gke-nodepool": "preemptible-pool", "cloud.google.com/gke-preemptible": "true"}, "preemptible-pool", "gke", "cloud.google.com/gke-nodepool=preemptible-pool", true},
		{map[string]string{"kubernetes.azure.com/agentpool": "user1", "kubernetes.azure.com/scalesetpriority": "spot"}, "user1", "aks", "kubernetes.azure.com/agentpool=user1", true},
		// Karpenter on AKS (NAP) or on GKE: the Karpenter label wins.
		{map[string]string{"kubernetes.azure.com/agentpool": "x", "karpenter.sh/nodepool": "default"}, "default", "karpenter", "karpenter.sh/nodepool=default", false},
		{map[string]string{"cloud.google.com/gke-nodepool": "x", "karpenter.sh/nodepool": "default"}, "default", "karpenter", "karpenter.sh/nodepool=default", false},
		{map[string]string{}, "other nodes", "", "", false},
	}
	for _, c := range cases {
		p, s, sel := poolLabel(c.labels)
		if p != c.pool || s != c.src || sel != c.selector {
			t.Errorf("poolLabel(%v) = %q,%q,%q; want %q,%q,%q", c.labels, p, s, sel, c.pool, c.src, c.selector)
		}
		if p2, s2 := poolOf(c.labels); p2 != p || s2 != s {
			t.Errorf("poolOf(%v) = %q,%q; poolLabel says %q,%q", c.labels, p2, s2, p, s)
		}
		if got := isSpot(c.labels); got != c.spot {
			t.Errorf("isSpot(%v) = %v; want %v", c.labels, got, c.spot)
		}
	}
}

func nodeObj(name string, labels map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Node"}}
	u.SetName(name)
	u.SetLabels(labels)
	return u
}

// The nodes table names the pool whatever the platform, marks spot nodes, and has a
// CLASS column only on a cluster where some node has a ComputeClass.
func TestNodeColumns(t *testing.T) {
	nodes := Kind{Name: "nodes", Kind: "Node", Version: "v1"}
	gke := toRow(nodes, nodeObj("n1", map[string]string{"cloud.google.com/gke-nodepool": "nap-x", "cloud.google.com/compute-class": "my-class", "cloud.google.com/gke-spot": "true"}))
	if gke.Cells["NODEPOOL"] != "nap-x" || gke.Cells["CLASS"] != "my-class" || gke.Cells["SPOT"] != "spot" {
		t.Errorf("GKE node cells: %v", gke.Cells)
	}
	eks := toRow(nodes, nodeObj("n2", map[string]string{"eks.amazonaws.com/nodegroup": "ng-1"}))
	if eks.Cells["NODEPOOL"] != "ng-1" || eks.Cells["CLASS"] != "" || eks.Cells["SPOT"] != "" {
		t.Errorf("EKS node cells: %v", eks.Cells)
	}
	if bare := toRow(nodes, nodeObj("n3", nil)); bare.Cells["NODEPOOL"] != "" {
		t.Errorf("a node in no pool must have an empty NODEPOOL, got %q", bare.Cells["NODEPOOL"])
	}

	cols := columnsFor(nodes)
	if got := dropIfEmpty(cols, []Row{eks}, "CLASS"); slices.Contains(got, "CLASS") || !slices.Contains(got, "SPOT") || len(got) != len(cols)-1 {
		t.Errorf("no node has a class: CLASS should go, the rest stay: %v", got)
	}
	if got := dropIfEmpty(cols, []Row{eks, gke}, "CLASS"); !slices.Contains(got, "CLASS") {
		t.Errorf("one node has a class: CLASS should stay: %v", got)
	}
}

// Pools of different sources that share a name stay separate, and every pool
// carries the label that selects its nodes.
func TestClusterMapPoolSources(t *testing.T) {
	node := func(name string, labels map[string]string) *corev1.Node {
		return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	}
	cs := kubefake.NewSimpleClientset(
		node("a1", map[string]string{"kubernetes.azure.com/agentpool": "default"}),
		node("a2", map[string]string{"kubernetes.azure.com/agentpool": "default"}),
		node("k1", map[string]string{"kubernetes.azure.com/agentpool": "x", "karpenter.sh/nodepool": "default"}),
		node("g1", map[string]string{"cloud.google.com/gke-nodepool": "nap-x", "cloud.google.com/compute-class": "my-class", "cloud.google.com/gke-spot": "true"}),
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"}, Spec: corev1.PodSpec{NodeName: "k1"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "services"}: "List", {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}: "List"})
	cm, err := (&Cluster{Typed: cs, Dynamic: dyn}).ClusterMap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]MapPool{}
	for _, p := range cm.Pools {
		got[p.Name] = p
	}
	if len(cm.Pools) != 3 {
		t.Fatalf("want 3 pools, got %d: %+v", len(cm.Pools), cm.Pools)
	}
	if p := got["default"]; p.Source != "aks" || len(p.Nodes) != 2 || p.Selector != "kubernetes.azure.com/agentpool=default" {
		t.Errorf("AKS pool: %+v", p)
	}
	if p := got["default (karpenter)"]; p.Source != "karpenter" || len(p.Nodes) != 1 || p.Selector != "karpenter.sh/nodepool=default" || p.Pods != 1 {
		t.Errorf("Karpenter pool of the same name: %+v", p)
	}
	if p := got["nap-x"]; p.Source != "gke-nap" || p.Class != "my-class" || len(p.Nodes) != 1 || !p.Nodes[0].Spot {
		t.Errorf("GKE NAP pool: %+v", p)
	}
	for _, l := range cm.Links {
		if l.Namespace == "shop" && l.Pool != "default (karpenter)" {
			t.Errorf("the pod on k1 belongs to the Karpenter pool, linked to %q", l.Pool)
		}
	}
}
