package chartgen

import (
	"strings"
	"testing"

	"github.com/ruddervirt/kubevirt/internal/manifest"
	"github.com/ruddervirt/kubevirt/internal/upstream"
)

const crdsTag = "v4.3.2"

// crdsFixture has the same layout as the upstream multus-daemonset.yml: a comment
// document, the CRD, and documents of other kinds that the chart drops.
const crdsFixture = `# Note:
#   quickstart file
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: network-attachment-definitions.k8s.cni.cncf.io
spec:
  group: k8s.cni.cncf.io
  scope: Namespaced
  names:
    plural: network-attachment-definitions
    singular: network-attachment-definition
    kind: NetworkAttachmentDefinition
    shortNames:
      - nad
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          description: 'literal {{ braces }}'
          type: object
---
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: multus
rules: []
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: multus
  namespace: kube-system
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kube-multus-ds
  namespace: kube-system
spec:
  template:
    spec:
      containers:
      - name: kube-multus
        image: ghcr.io/k8snetworkplumbingwg/multus-cni:snapshot
`

const nadCRD = "network-attachment-definitions.k8s.cni.cncf.io"

func genCRDs(t *testing.T, src string) (map[string][]byte, error) {
	t.Helper()
	p, err := upstream.Get("multus-crds")
	if err != nil {
		t.Fatal(err)
	}
	return Generate(Input{Project: p, Tag: crdsTag, Source: []byte(src)})
}

func TestGenerateCRDs(t *testing.T) {
	files, err := genCRDs(t, crdsFixture)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"Chart.yaml":                    true,
		"values.yaml":                   true,
		"templates/NOTES.txt":           true,
		"templates/" + nadCRD + ".yaml": true,
	}
	for f := range files {
		if !want[f] {
			t.Errorf("unexpected file %s", f)
		}
	}
	for f := range want {
		if _, ok := files[f]; !ok {
			t.Errorf("missing %s", f)
		}
	}

	crd := string(files["templates/"+nadCRD+".yaml"])
	for _, bad := range []string{"ClusterRole", "ServiceAccount", "DaemonSet", "kube-system", "image:"} {
		if strings.Contains(crd, bad) {
			t.Errorf("CRD template contains %q", bad)
		}
	}
	if !strings.Contains(crd, `literal {{ "{{" }} braces {{ "}}" }}`) {
		t.Errorf("literal braces are not escaped:\n%s", crd)
	}

	// Without the header, the keep annotation, and the brace escapes, the
	// template must be the upstream CRD.
	objs, err := manifest.Split([]byte(strings.NewReplacer(`{{ "{{" }}`, "{{", `{{ "}}" }}`, "}}").Replace(crd)))
	if err != nil || len(objs) != 1 {
		t.Fatalf("split CRD template: %d objects, %v", len(objs), err)
	}
	if got := manifest.Annotations(objs[0])[KeepAnnotation]; got != "keep" {
		t.Errorf("annotation %s = %v, want keep", KeepAnnotation, got)
	}
	manifest.RemoveAnnotations(objs[0], func(k string) bool { return k == KeepAnnotation })
	up, _ := manifest.Split([]byte(crdsFixture))
	if d := manifest.Diff(objs[0], up[0]); len(d) > 0 {
		t.Errorf("CRD differs from upstream:\n  %s", strings.Join(d, "\n  "))
	}

	chart := string(files["Chart.yaml"])
	if !strings.Contains(chart, "name: multus-crds\n") || !strings.Contains(chart, "version: 4.3.2\n") || !strings.Contains(chart, "appVersion: v4.3.2\n") {
		t.Errorf("Chart.yaml:\n%s", chart)
	}
}

func TestCRDsLayoutChanges(t *testing.T) {
	extraCRD := "---\napiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: multi-networkpolicies.k8s.cni.cncf.io\nspec:\n  group: k8s.cni.cncf.io\n"
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"renamed CRD", strings.Replace(crdsFixture, "name: "+nadCRD, "name: other.k8s.cni.cncf.io", 1), "new CustomResourceDefinition other.k8s.cni.cncf.io"},
		{"missing CRD", strings.Replace(crdsFixture, "kind: CustomResourceDefinition", "kind: ConfigMap", 1), "CustomResourceDefinition " + nadCRD + " not found"},
		{"unexpected extra CRD", crdsFixture + extraCRD, "new CustomResourceDefinition multi-networkpolicies.k8s.cni.cncf.io"},
		{"duplicate CRD", crdsFixture + "---\napiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: " + nadCRD + "\n", "duplicate"},
		{"namespaced CRD object", strings.Replace(crdsFixture, "  name: "+nadCRD+"\n", "  name: "+nadCRD+"\n  namespace: kube-system\n", 1), "metadata.namespace"},
		{"broken YAML", crdsFixture + "---\nkind: [\n", "document"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := genCRDs(t, c.src)
			if err == nil {
				t.Fatalf("no error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

func TestCRDsFixVersion(t *testing.T) {
	p, _ := upstream.Get("multus-crds")
	files, err := Generate(Input{Project: p, Tag: crdsTag, Fix: 1, Source: []byte(crdsFixture)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "version: 4.3.2+fix.1\n") {
		t.Errorf("Chart.yaml:\n%s", files["Chart.yaml"])
	}
}
