package tui

import (
	"strings"
	"testing"

	"github.com/njahangirov/kube-console/internal/kube"
)

func TestTruncateMiddle(t *testing.T) {
	for _, c := range []struct {
		in   string
		w    int
		want string
	}{
		{"short", 10, "short"},
		{"gke-prod-general-pool-a1b2c3d4-x9zq", 24, "gke-prod-ge…1b2c3d4-x9zq"},
		{"abcdefghij", 4, truncate("abcdefghij", 4)},
	} {
		if got := truncateMiddle(c.in, c.w); got != c.want {
			t.Errorf("truncateMiddle(%q, %d) = %q; want %q", c.in, c.w, got, c.want)
		}
	}
}

// A nodes table that does not fit drops CLASS and SPOT before it cuts anything, and
// what is left of a node's name still ends in the part that tells nodes apart.
func TestNodesTableNarrow(t *testing.T) {
	row := func(name string) kube.ClusterRow {
		return kube.ClusterRow{Row: kube.Row{Name: name, Age: "2d", Status: "Ready", Health: kube.Healthy, Cells: map[string]string{
			"ROLES": "<none>", "VERSION": "v1.33.1-gke.1000000", "INSTANCE": "n2-standard-8", "ZONE": "us-east1-b",
			"NODEPOOL": "nap-n2-standard-8-spot-x1y2z3", "CLASS": "my-class", "SPOT": "spot"}}}
	}
	m := &Model{kind: kube.Kind{Name: "nodes", Kind: "Node"},
		cols:    []string{"ROLES", "VERSION", "INSTANCE", "ZONE", "NODEPOOL", "CLASS", "SPOT"},
		rows:    []kube.ClusterRow{row("gke-prod-nap-n2-standard-8-sp-a1b2c3d4-x9zq"), row("gke-prod-nap-n2-standard-8-sp-a1b2c3d4-k7mw")},
		visible: []int{0, 1}}

	m.width = 200
	wide := stripANSI(m.tableView(10))
	for _, want := range []string{"CLASS", "SPOT", "my-class", "gke-prod-nap-n2-standard-8-sp-a1b2c3d4-x9zq"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide table: missing %q in:\n%s", want, wide)
		}
	}

	// A little too narrow: CLASS goes, SPOT still fits and stays.
	m.width = 155
	mid := stripANSI(m.tableView(10))
	if strings.Contains(mid, "CLASS") || !strings.Contains(mid, "SPOT") || !strings.Contains(mid, "gke-prod-nap-n2-standard-8-sp-a1b2c3d4-x9zq") {
		t.Errorf("at 155 columns CLASS alone should be dropped and names stay whole:\n%s", mid)
	}

	// Another kind is left alone: its table has no optional columns and names are cut at the end.
	pods := &Model{kind: kube.Kind{Name: "pods", Kind: "Pod", Namespaced: true}, cols: []string{"CLASS", "SPOT"}, width: 40,
		rows: []kube.ClusterRow{{Row: kube.Row{Namespace: "shop", Name: "a-pod-with-a-very-long-name-that-cannot-fit-x9zq", Age: "2d", Status: "Running",
			Cells: map[string]string{"CLASS": "c", "SPOT": "s"}}}}, visible: []int{0}}
	if out := stripANSI(pods.tableView(10)); !strings.Contains(out, "CLASS") || !strings.Contains(out, "SPOT") || strings.Contains(out, "x9zq") {
		t.Errorf("pods table must keep its columns and cut names at the end:\n%s", out)
	}

	m.width = 110
	narrow := stripANSI(m.tableView(10))
	for _, gone := range []string{"CLASS", "SPOT", "my-class"} {
		if strings.Contains(narrow, gone) {
			t.Errorf("narrow table: %q should have been dropped:\n%s", gone, narrow)
		}
	}
	for _, want := range []string{"NODEPOOL", "-x9zq", "-k7mw", "gke-"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("narrow table: missing %q in:\n%s", want, narrow)
		}
	}
}
