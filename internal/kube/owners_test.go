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

func ownerCluster(objs ...runtime.Object) *Cluster {
	gvrs := map[schema.GroupVersionResource]string{podsGVR: "PodList"}
	for _, k := range knownOwnerKinds {
		gvrs[k.GVR()] = k.Kind + "List"
	}
	return &Cluster{Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, objs...)}
}

// owned builds an object; owner is "apiVersion/Kind/name/uid" or "".
func owned(apiVersion, kind, ns, name, uid, owner string) *unstructured.Unstructured {
	md := map[string]any{"name": name, "namespace": ns, "uid": uid}
	if owner != "" {
		p := strings.Split(owner, "|")
		md["ownerReferences"] = []any{map[string]any{"apiVersion": p[0], "kind": p[1], "name": p[2], "uid": p[3], "controller": true}}
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": md, "spec": map[string]any{}}}
}

func chainText(l []OwnerLink) string {
	var out []string
	for _, x := range l {
		s := x.KindID + "/" + x.Name
		if x.Missing {
			s += "(missing)"
		}
		out = append(out, s)
	}
	return strings.Join(out, " > ")
}

func TestOwnerChain(t *testing.T) {
	c := ownerCluster(
		owned("apps/v1", "Deployment", "shop", "web", "u-dep", ""),
		owned("apps/v1", "ReplicaSet", "shop", "web-7d9f", "u-rs", "apps/v1|Deployment|web|u-dep"),
		owned("v1", "Pod", "shop", "web-7d9f-abcde", "u-p1", "apps/v1|ReplicaSet|web-7d9f|u-rs"),
		owned("apps/v1", "StatefulSet", "shop", "db", "u-sts", ""),
		owned("v1", "Pod", "shop", "db-0", "u-p2", "apps/v1|StatefulSet|db|u-sts"),
		owned("batch/v1", "CronJob", "shop", "report", "u-cj", ""),
		owned("batch/v1", "Job", "shop", "report-2911", "u-job", "batch/v1|CronJob|report|u-cj"),
		owned("v1", "Pod", "shop", "report-2911-x", "u-p3", "batch/v1|Job|report-2911|u-job"),
		owned("v1", "Pod", "shop", "debug", "u-p4", ""),
		// orphan: its ReplicaSet was deleted
		owned("v1", "Pod", "shop", "old-abc", "u-p5", "apps/v1|ReplicaSet|old|u-gone"),
		// same name, new uid: not the owner any more
		owned("apps/v1", "Deployment", "shop", "api", "u-api-new", ""),
		owned("apps/v1", "ReplicaSet", "shop", "api-1", "u-rs2", "apps/v1|Deployment|api|u-api-old"),
	)
	pod := Kind{Name: "pods", Version: "v1", Namespaced: true}
	rs := Kind{Name: "replicasets", Group: "apps", Version: "v1", Namespaced: true}
	cases := []struct {
		k    Kind
		name string
		want string
	}{
		{pod, "web-7d9f-abcde", "replicasets.apps/web-7d9f > deployments.apps/web"},
		{rs, "web-7d9f", "deployments.apps/web"},
		{pod, "db-0", "statefulsets.apps/db"},
		{pod, "report-2911-x", "jobs.batch/report-2911 > cronjobs.batch/report"},
		{pod, "debug", ""},
		{pod, "old-abc", "replicasets.apps/old(missing)"},
		{rs, "api-1", "deployments.apps/api(missing)"},
	}
	for _, tc := range cases {
		got, err := c.OwnerChain(context.Background(), tc.k, "shop", tc.name)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if s := chainText(got); s != tc.want {
			t.Errorf("%s: chain %q, want %q", tc.name, s, tc.want)
		}
	}
	got, _ := c.OwnerChain(context.Background(), pod, "shop", "web-7d9f-abcde")
	if got[0].Namespace != "shop" || got[0].Row == nil || !got[0].Controller {
		t.Errorf("first link: %+v", got[0])
	}
}
