package kube

import "testing"

func TestPoolOf(t *testing.T) {
	cases := []struct {
		labels    map[string]string
		pool, src string
		spot      bool
	}{
		{map[string]string{"karpenter.sh/nodepool": "default", "karpenter.sh/capacity-type": "spot"}, "default", "karpenter", true},
		{map[string]string{"eks.amazonaws.com/nodegroup": "ng-1"}, "ng-1", "eks", false},
		{map[string]string{"cloud.google.com/gke-nodepool": "general-pool-a1b2"}, "general-pool-a1b2", "gke", false},
		{map[string]string{"cloud.google.com/gke-nodepool": "nap-n2-standard-8-spot-x1y2z3", "cloud.google.com/gke-spot": "true"},
			"nap-n2-standard-8-spot-x1y2z3", "gke-nap", true},
		{map[string]string{"cloud.google.com/gke-nodepool": "preemptible-pool", "cloud.google.com/gke-preemptible": "true"}, "preemptible-pool", "gke", true},
		{map[string]string{"kubernetes.azure.com/agentpool": "user1", "kubernetes.azure.com/scalesetpriority": "spot"}, "user1", "aks", true},
		// AKS NAP runs Karpenter: the Karpenter label wins.
		{map[string]string{"kubernetes.azure.com/agentpool": "x", "karpenter.sh/nodepool": "default"}, "default", "karpenter", false},
		{map[string]string{}, "other nodes", "", false},
	}
	for _, c := range cases {
		p, s := poolOf(c.labels)
		if p != c.pool || s != c.src {
			t.Errorf("poolOf(%v) = %q,%q; want %q,%q", c.labels, p, s, c.pool, c.src)
		}
		if got := isSpot(c.labels); got != c.spot {
			t.Errorf("isSpot(%v) = %v; want %v", c.labels, got, c.spot)
		}
	}
}
