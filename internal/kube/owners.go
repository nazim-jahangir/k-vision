package kube

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// OwnerLink is one step up an object's ownerReferences: the Pod's ReplicaSet, the
// ReplicaSet's Deployment, the Job's CronJob. Row is nil when the owner no longer
// exists (an orphan whose controller was deleted with --cascade=orphan, for example).
type OwnerLink struct {
	Kind       string `json:"kind"`   // ReplicaSet
	KindID     string `json:"kindId"` // replicasets.apps -- what the UI opens
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	Controller bool   `json:"controller"`
	Row        *Row   `json:"row,omitempty"`
	Missing    bool   `json:"missing,omitempty"`
	Error      string `json:"error,omitempty"`
}

// The workload kinds owners are nearly always. Known here, so the common chains need no
// API discovery; anything else (Argo Rollouts, operators' CRDs) goes through the mapper.
var knownOwnerKinds = map[string]Kind{
	"apps/ReplicaSet":         {Name: "replicasets", Group: "apps", Version: "v1", Kind: "ReplicaSet", Namespaced: true},
	"apps/Deployment":         {Name: "deployments", Group: "apps", Version: "v1", Kind: "Deployment", Namespaced: true},
	"apps/StatefulSet":        {Name: "statefulsets", Group: "apps", Version: "v1", Kind: "StatefulSet", Namespaced: true},
	"apps/DaemonSet":          {Name: "daemonsets", Group: "apps", Version: "v1", Kind: "DaemonSet", Namespaced: true},
	"apps/ControllerRevision": {Name: "controllerrevisions", Group: "apps", Version: "v1", Kind: "ControllerRevision", Namespaced: true},
	"batch/Job":               {Name: "jobs", Group: "batch", Version: "v1", Kind: "Job", Namespaced: true},
	"batch/CronJob":           {Name: "cronjobs", Group: "batch", Version: "v1", Kind: "CronJob", Namespaced: true},
	"/Node":                   {Name: "nodes", Version: "v1", Kind: "Node"}, // owns static (mirror) pods
	"/ReplicationController":  {Name: "replicationcontrollers", Version: "v1", Kind: "ReplicationController", Namespaced: true},
}

func (c *Cluster) ownerKind(apiVersion, kind string) (Kind, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return Kind{}, err
	}
	if k, ok := knownOwnerKinds[gv.Group+"/"+kind]; ok {
		return k, nil
	}
	if c.Mapper == nil {
		return Kind{}, fmt.Errorf("unknown owner kind %s %s", apiVersion, kind)
	}
	m, err := c.MappingFor(gv.WithKind(kind))
	if err != nil {
		return Kind{}, err
	}
	return Kind{Name: m.Resource.Resource, Group: m.Resource.Group, Version: m.Resource.Version, Kind: kind,
		Namespaced: m.Scope.Name() == "namespace"}, nil
}

// controllerRef is the owner that manages the object (controller: true), else the first owner.
func controllerRef(u *unstructured.Unstructured) *metav1.OwnerReference {
	refs := u.GetOwnerReferences()
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	if len(refs) > 0 {
		return &refs[0]
	}
	return nil
}

// OwnerChain walks up from an object to the top of its controller chain, e.g.
// Pod -> ReplicaSet -> Deployment, Pod -> StatefulSet, Pod -> Job -> CronJob. Empty for
// an object nothing owns (a bare pod, a Deployment). Stops at an owner that is gone.
func (c *Cluster) OwnerChain(ctx context.Context, k Kind, namespace, name string) ([]OwnerLink, error) {
	u, err := c.Get(ctx, k, namespace, name)
	if err != nil {
		return nil, err
	}
	chain := []OwnerLink{}
	seen := map[string]bool{}
	for depth := 0; depth < 6; depth++ { // real chains are 1-3 deep; the cap guards against loops
		ref := controllerRef(u)
		if ref == nil {
			break
		}
		link := OwnerLink{Kind: ref.Kind, Name: ref.Name, Controller: ref.Controller != nil && *ref.Controller}
		ok, err := c.ownerKind(ref.APIVersion, ref.Kind)
		if err != nil {
			link.KindID, link.Error = strings.ToLower(ref.Kind), err.Error()
			chain = append(chain, link)
			break
		}
		link.KindID = ok.ID()
		if ok.Namespaced {
			link.Namespace = u.GetNamespace() // owners are always in the dependent's namespace (or cluster-scoped)
		}
		key := link.KindID + "/" + link.Namespace + "/" + link.Name
		if seen[key] {
			break
		}
		seen[key] = true
		owner, err := c.Get(ctx, ok, link.Namespace, link.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				link.Missing = true
			} else {
				link.Error = err.Error()
			}
			chain = append(chain, link)
			break
		}
		// The ownerReference carries the owner's uid: a same-named object created later is
		// not this object's owner.
		if string(ref.UID) != "" && owner.GetUID() != ref.UID {
			link.Missing = true
			chain = append(chain, link)
			break
		}
		r := toRow(ok, owner)
		link.Row = &r
		chain = append(chain, link)
		u = owner
	}
	return chain, nil
}
