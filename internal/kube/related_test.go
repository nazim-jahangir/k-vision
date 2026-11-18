package kube

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func relatedCluster(objs ...runtime.Object) *Cluster {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		podsGVR:       "PodList",
		pvcGVR:        "PersistentVolumeClaimList",
		pvGVR:         "PersistentVolumeList",
		podMetricsGVR: "PodMetricsList",
	}, objs...)
	return &Cluster{Dynamic: dyn}
}

func relObj(apiVersion, kind, ns, name string, spec map[string]any, extra ...map[string]any) *unstructured.Unstructured {
	md := map[string]any{"name": name}
	if ns != "" {
		md["namespace"] = ns
	}
	o := map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": md, "spec": spec}
	for _, e := range extra {
		for k, v := range e {
			o[k] = v
		}
	}
	return &unstructured.Unstructured{Object: o}
}

func relNames(rows []Row) string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Namespace+"/"+r.Name)
	}
	return strings.Join(out, ",")
}

func TestPodsOnNode(t *testing.T) {
	pod := func(ns, name, node string) *unstructured.Unstructured {
		return relObj("v1", "Pod", ns, name, map[string]any{"nodeName": node})
	}
	// The fake client ignores field selectors, so this also checks the exact-name filter.
	c := relatedCluster(pod("b", "web-2", "n1"), pod("a", "web-1", "n1"), pod("a", "db-1", "n2"), pod("a", "pending", ""))
	rows, err := c.PodsOnNode(context.Background(), "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got := relNames(rows); got != "a/web-1,b/web-2" {
		t.Fatalf("pods on n1: %s", got)
	}
	if rows[0].Cells["CPU"] != "-" {
		t.Fatalf("no metrics should show as -: %q", rows[0].Cells["CPU"])
	}
}

func TestMountsClaim(t *testing.T) {
	vols := func(v ...any) map[string]any { return map[string]any{"spec": map[string]any{"volumes": v}} }
	direct := vols(map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "data-db-0"}})
	eph := vols(map[string]any{"name": "scratch", "ephemeral": map[string]any{}})
	other := vols(map[string]any{"name": "cfg", "configMap": map[string]any{"name": "data-db-0"}})
	cases := []struct {
		pod   map[string]any
		name  string
		claim string
		want  bool
	}{
		{direct, "db-0", "data-db-0", true},
		{direct, "db-0", "data-db-1", false},
		{eph, "job-x", "job-x-scratch", true},
		{eph, "job-y", "job-x-scratch", false},
		{other, "db-0", "data-db-0", false}, // a ConfigMap with the same name is not the claim
		{map[string]any{}, "p", "c", false},
	}
	for i, tc := range cases {
		if got := mountsClaim(tc.pod, tc.name, tc.claim); got != tc.want {
			t.Errorf("case %d: mountsClaim(%s, %s) = %v", i, tc.name, tc.claim, got)
		}
	}
}

func TestClaimLinks(t *testing.T) {
	pvc := relObj("v1", "PersistentVolumeClaim", "db", "data-db-0", map[string]any{"volumeName": "pvc-123", "storageClassName": "gp3"},
		map[string]any{"status": map[string]any{"phase": "Bound", "capacity": map[string]any{"storage": "10Gi"}}})
	pending := relObj("v1", "PersistentVolumeClaim", "db", "new", map[string]any{}, map[string]any{"status": map[string]any{"phase": "Pending"}})
	pv := relObj("v1", "PersistentVolume", "", "pvc-123", map[string]any{
		"capacity": map[string]any{"storage": "10Gi"}, "accessModes": []any{"ReadWriteOnce"},
		"persistentVolumeReclaimPolicy": "Delete", "storageClassName": "gp3",
		"claimRef": map[string]any{"namespace": "db", "name": "data-db-0"}},
		map[string]any{"status": map[string]any{"phase": "Bound"}})
	user := relObj("v1", "Pod", "db", "db-0", map[string]any{"volumes": []any{
		map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "data-db-0"}}}})
	bystander := relObj("v1", "Pod", "db", "web", map[string]any{})
	c := relatedCluster(pvc, pending, pv, user, bystander)
	ctx := context.Background()

	l, err := c.ClaimLinks(ctx, "db", "data-db-0")
	if err != nil {
		t.Fatal(err)
	}
	if l.Volume == nil || l.Volume.Name != "pvc-123" || l.Volume.Status != "Bound" || l.Volume.Cells["ACCESS"] != "RWO" {
		t.Fatalf("volume of the claim: %+v", l.Volume)
	}
	if relNames(l.Pods) != "db/db-0" {
		t.Fatalf("pods of the claim: %s", relNames(l.Pods))
	}

	l, err = c.ClaimLinks(ctx, "db", "new")
	if err != nil {
		t.Fatal(err)
	}
	if l.Volume != nil || l.VolumeName != "" || len(l.Pods) != 0 {
		t.Fatalf("a pending claim has no volume and no pods: %+v", l)
	}

	l, err = c.VolumeClaimLinks(ctx, "pvc-123")
	if err != nil {
		t.Fatal(err)
	}
	if l.Claim == nil || l.Claim.Name != "data-db-0" || l.ClaimNS != "db" || relNames(l.Pods) != "db/db-0" {
		t.Fatalf("links of the volume: %+v", l)
	}
	if l.Volume.Cells["CLAIM"] != "db/data-db-0" {
		t.Fatalf("PV CLAIM cell: %q", l.Volume.Cells["CLAIM"])
	}
}
