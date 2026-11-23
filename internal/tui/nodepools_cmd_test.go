package tui

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/cached/memory"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/njahangirov/kube-console/internal/kube"
)

func modelServing(lists ...*metav1.APIResourceList) *Model {
	lists = append(lists, &metav1.APIResourceList{GroupVersion: "v1", APIResources: []metav1.APIResource{
		{Name: "nodes", Kind: "Node", Verbs: []string{"list"}}}})
	disco := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: lists}}
	return &Model{cluster: &kube.Cluster{Discovery: memory.NewMemCacheClient(disco)}, kind: kube.Kind{Name: "nodes", Kind: "Node"}}
}

func nodepoolsCRD(groupVersion string) *metav1.APIResourceList {
	return &metav1.APIResourceList{GroupVersion: groupVersion, APIResources: []metav1.APIResource{
		{Name: "nodepools", Kind: "NodePool", Verbs: []string{"list"}}}}
}

// :nodepools lists whatever resource type the cluster calls nodepools, Karpenter's or
// another one, and opens Cluster Topology only where there is none.
func TestNodepoolsCommand(t *testing.T) {
	for _, c := range []struct {
		name     string
		lists    []*metav1.APIResourceList
		wantKind string // "" = Cluster Topology
	}{
		{"karpenter", []*metav1.APIResourceList{nodepoolsCRD("karpenter.sh/v1")}, "nodepools.karpenter.sh"},
		{"another nodepools CRD", []*metav1.APIResourceList{nodepoolsCRD("container.example.io/v1")}, "nodepools.container.example.io"},
		{"none", nil, ""},
	} {
		for _, cmd := range []string{"nodepools", "nodepool"} {
			m := modelServing(c.lists...)
			run := m.command(cmd)
			if run == nil || m.isErr {
				t.Errorf("%s: :%s failed: %q", c.name, cmd, m.status)
				continue
			}
			if c.wantKind == "" {
				if m.kind.ID() != "nodes" || m.status != "mapping the cluster…" {
					t.Errorf("%s: :%s should open Cluster Topology, got kind %q status %q", c.name, cmd, m.kind.ID(), m.status)
				}
			} else if m.kind.ID() != c.wantKind {
				t.Errorf("%s: :%s opened %q, want %q", c.name, cmd, m.kind.ID(), c.wantKind)
			}
		}
	}

	// Any other unknown name is still an error, not a redirect.
	m := modelServing()
	if run := m.command("nosuchkind"); run != nil || !m.isErr {
		t.Errorf(":nosuchkind should fail, got status %q", m.status)
	}
}
