// Package merge documents and tests how Helm patches the custom resource when Helm
// uses client-side apply. That is the mode that k3s helm-controller uses when a
// HelmChart sets takeOwnership: true (it passes --take-ownership and
// --server-side=false to Helm v4).
//
// Adoption ("helm install --take-ownership", Helm v3.17+ and v4): Helm passes the
// rendered resources as the "original" objects (pkg/action/validate.go
// requireAdoption) and builds a three-way JSON merge patch
// (jsonmergepatch.CreateThreeWayJSONMergePatch) from original, the rendered
// manifest, and the live object. Because original == rendered, the patch can only
// add or change fields that the chart renders. It never deletes a live field.
//
// Upgrade ("helm upgrade"): Helm builds a two-way JSON merge patch
// (jsonpatch.CreateMergePatch) from the previous release manifest and the new
// manifest (pkg/kube/client.go createPatch). A field that is in neither manifest
// is not touched. A field that the previous release rendered and the new release
// does not render is deleted. Lists are replaced as a whole.
//
// The e2e test (test/e2e/run.sh) checks the same behavior on a real cluster.
package merge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"k8s.io/apimachinery/pkg/util/jsonmergepatch"
	"k8s.io/apimachinery/pkg/util/mergepatch"
	"sigs.k8s.io/yaml"
)

// chart is the KubeVirt CR that the chart renders with default values.
const chart = `
apiVersion: kubevirt.io/v1
kind: KubeVirt
metadata:
  name: kubevirt
  namespace: kubevirt
  annotations:
    helm.sh/resource-policy: keep
spec:
  configuration:
    imagePullPolicy: IfNotPresent
  imagePullPolicy: IfNotPresent
  uninstallStrategy: BlockUninstallIfWorkloadsExist
`

// live is the CR after "kubectl apply -f kubevirt-cr.yaml" and an Aileron patch.
const live = `
apiVersion: kubevirt.io/v1
kind: KubeVirt
metadata:
  name: kubevirt
  namespace: kubevirt
  resourceVersion: "1234"
spec:
  certificateRotateStrategy: {}
  configuration:
    developerConfiguration:
      featureGates: [Snapshot, HostDevices]
    imagePullPolicy: IfNotPresent
  customizeComponents: {}
  imagePullPolicy: IfNotPresent
  workloadUpdateStrategy: {}
status:
  phase: Deployed
`

func toJSON(t *testing.T, y string) []byte {
	t.Helper()
	b, err := yaml.YAMLToJSON([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// adopt computes the patch the way "helm install --take-ownership" does and applies it to current.
func adopt(t *testing.T, original, modified, current string) map[string]any {
	t.Helper()
	pre := []mergepatch.PreconditionFunc{
		mergepatch.RequireKeyUnchanged("apiVersion"),
		mergepatch.RequireKeyUnchanged("kind"),
		mergepatch.RequireMetadataKeyUnchanged("name"),
	}
	cur := toJSON(t, current)
	patch, err := jsonmergepatch.CreateThreeWayJSONMergePatch(toJSON(t, original), toJSON(t, modified), cur, pre...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonpatch.MergePatch(cur, patch)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// upgrade computes the patch the way "helm upgrade" does and applies it to current.
func upgrade(t *testing.T, previous, next, current string) map[string]any {
	t.Helper()
	cur := toJSON(t, current)
	patch, err := jsonpatch.CreateMergePatch(toJSON(t, previous), toJSON(t, next))
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonpatch.MergePatch(cur, patch)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func gates(obj map[string]any) any {
	spec, _ := obj["spec"].(map[string]any)
	conf, _ := spec["configuration"].(map[string]any)
	dev, _ := conf["developerConfiguration"].(map[string]any)
	return dev["featureGates"]
}

func TestAdoptionKeepsUnrenderedFields(t *testing.T) {
	got := adopt(t, chart, chart, live)
	if want := []any{"Snapshot", "HostDevices"}; !reflect.DeepEqual(gates(got), want) {
		t.Errorf("featureGates = %v, want %v", gates(got), want)
	}
	spec := got["spec"].(map[string]any)
	if spec["uninstallStrategy"] != "BlockUninstallIfWorkloadsExist" {
		t.Errorf("uninstallStrategy not set: %v", spec)
	}
	if _, ok := spec["workloadUpdateStrategy"]; !ok {
		t.Errorf("adoption deleted the live field workloadUpdateStrategy")
	}
}

func TestUpgradeKeepsUnrenderedFields(t *testing.T) {
	// The new chart version changes a rendered field; featureGates is in neither manifest.
	next := strings.Replace(chart, "imagePullPolicy: IfNotPresent\n  uninstallStrategy", "imagePullPolicy: Always\n  uninstallStrategy", 1)
	got := upgrade(t, chart, next, live)
	if got["spec"].(map[string]any)["imagePullPolicy"] != "Always" {
		t.Errorf("upgrade did not change imagePullPolicy")
	}
	if want := []any{"Snapshot", "HostDevices"}; !reflect.DeepEqual(gates(got), want) {
		t.Errorf("featureGates = %v, want %v", gates(got), want)
	}
}

func withGates(gates string) string {
	return `
apiVersion: kubevirt.io/v1
kind: KubeVirt
metadata:
  name: kubevirt
  namespace: kubevirt
spec:
  configuration:
    developerConfiguration:
      featureGates: ` + gates + `
    imagePullPolicy: IfNotPresent
  imagePullPolicy: IfNotPresent
  uninstallStrategy: BlockUninstallIfWorkloadsExist
`
}

// A value that sets featureGates makes Helm own the whole list. Lists are replaced, not merged.
func TestValueOwnsFeatureGates(t *testing.T) {
	got := adopt(t, withGates("[ExpandDisks]"), withGates("[ExpandDisks]"), live)
	if want := []any{"ExpandDisks"}; !reflect.DeepEqual(gates(got), want) {
		t.Errorf("adoption: featureGates = %v, want %v", gates(got), want)
	}
	got = upgrade(t, chart, withGates("[ExpandDisks]"), live)
	if want := []any{"ExpandDisks"}; !reflect.DeepEqual(gates(got), want) {
		t.Errorf("featureGates = %v, want %v", gates(got), want)
	}
}

// If the previous release set featureGates and the new one does not, Helm deletes the field.
func TestUnsetValueDeletesField(t *testing.T) {
	got := upgrade(t, withGates("[ExpandDisks]"), chart, withGates("[ExpandDisks, Snapshot]"))
	if g := gates(got); g != nil {
		t.Errorf("featureGates = %v, want deleted", g)
	}
}

// An empty list in the chart would also own the field: on adoption it wipes live gates.
// This is why the generator drops "featureGates: []" from the upstream defaults.
func TestEmptyListWipesGates(t *testing.T) {
	got := adopt(t, withGates("[]"), withGates("[]"), live)
	if g := gates(got); !reflect.DeepEqual(g, []any{}) {
		t.Errorf("featureGates = %v, want []", g)
	}
}
