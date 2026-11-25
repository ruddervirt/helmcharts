package chartgen

import (
	"strings"
	"testing"

	"github.com/ruddervirt/kubevirt/internal/upstream"
)

const tag = "v1.9.1"

// fixture is a small operator manifest with the same layout as the upstream one.
const fixture = `---
apiVersion: v1
kind: Namespace
metadata:
  labels:
    kubevirt.io: ""
    pod-security.kubernetes.io/enforce: "privileged"
  name: kubevirt
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: kubevirts.kubevirt.io
spec:
  group: kubevirt.io
  description: "example namespace: kubevirt and a {{ brace }}"
---
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: kubevirt-cluster-critical
value: 1000000000
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubevirt.io:operator
rules:
- apiGroups: ["kubevirt.io"]
  resources: ["kubevirts"]
  resourceNames: ["kubevirt"]
  verbs: ["get"]
  description: "literal {{ braces }}"
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kubevirt-operator
  namespace: kubevirt
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kubevirt-operator
  namespace: kubevirt
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubevirt-operator-rolebinding
  namespace: kubevirt
subjects:
- kind: ServiceAccount
  name: kubevirt-operator
  namespace: kubevirt
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubevirt-operator
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubevirt-operator
subjects:
- kind: ServiceAccount
  name: kubevirt-operator
  namespace: kubevirt
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: virt-operator
  namespace: kubevirt
spec:
  template:
    spec:
      containers:
      - name: virt-operator
        image: quay.io/kubevirt/virt-operator:v1.9.1
        env:
        - name: VIRT_OPERATOR_IMAGE
          value: quay.io/kubevirt/virt-operator:v1.9.1
        - name: KUBEVIRT_VERSION
          value: v1.9.1
`

const fixtureCR = `---
apiVersion: kubevirt.io/v1
kind: KubeVirt
metadata:
  name: kubevirt
  namespace: kubevirt
spec:
  certificateRotateStrategy: {}
  configuration:
    developerConfiguration:
      featureGates: []
    imagePullPolicy: IfNotPresent
  imagePullPolicy: IfNotPresent
`

func gen(t *testing.T, op, cr string) (map[string][]byte, error) {
	t.Helper()
	p, _ := upstream.Get("kubevirt")
	return Generate(Input{Project: p, Tag: tag, Operator: []byte(op), CR: []byte(cr)})
}

func TestGenerate(t *testing.T) {
	files, err := gen(t, fixture, fixtureCR)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"Chart.yaml", "values.yaml", "crds/kubevirts.kubevirt.io.yaml",
		"templates/kubevirt-operator.yaml", "templates/kubevirt.yaml",
		"templates/hook-crd.yaml", "templates/hook-namespace.yaml", "templates/_helpers.tpl",
	} {
		if _, ok := files[f]; !ok {
			t.Errorf("missing %s", f)
		}
	}
	op := string(files["templates/kubevirt-operator.yaml"])
	for _, want := range []string{
		`namespace: {{ .Release.Namespace }}`,
		`image: {{ include "kubevirt.image" (list . "virt-operator") | quote }}`,
		`value: {{ .Values.image.tag | quote }}`,
		`literal {{ "{{" }} braces {{ "}}" }}`,
		`helm.sh/resource-policy: keep`,
	} {
		if !strings.Contains(op, want) {
			t.Errorf("operator template does not contain %q", want)
		}
	}
	body := op[strings.Index(op, "\n")+1:] // skip the "Code generated" header
	for _, bad := range []string{"kind: Namespace", "kind: CustomResourceDefinition", "v1.9.1", "namespace: kubevirt"} {
		if strings.Contains(body, bad) {
			t.Errorf("operator template contains %q", bad)
		}
	}
	// The CRD is not a template: braces stay literal.
	if !strings.Contains(string(files["crds/kubevirts.kubevirt.io.yaml"]), "{{ brace }}") {
		t.Errorf("CRD changed")
	}
	values := string(files["values.yaml"])
	if strings.Contains(values, "featureGates") || strings.Contains(values, "certificateRotateStrategy") {
		t.Errorf("values.yaml renders empty upstream fields:\n%s", values)
	}
	if !strings.Contains(values, "uninstallStrategy: BlockUninstallIfWorkloadsExist") {
		t.Errorf("values.yaml has no uninstallStrategy")
	}
	if !strings.Contains(values, "tag: v1.9.1") {
		t.Errorf("values.yaml has no tag with the v prefix")
	}
	chart := string(files["Chart.yaml"])
	if !strings.Contains(chart, "version: 1.9.1\n") || !strings.Contains(chart, "appVersion: v1.9.1\n") {
		t.Errorf("Chart.yaml versions:\n%s", chart)
	}
	ns := string(files["templates/hook-namespace.yaml"])
	if !strings.Contains(ns, `"kubevirt.io="`) || !strings.Contains(ns, `"pod-security.kubernetes.io/enforce=privileged"`) {
		t.Errorf("namespace hook labels:\n%s", ns)
	}
}

func TestFixVersion(t *testing.T) {
	p, _ := upstream.Get("kubevirt")
	files, err := Generate(Input{Project: p, Tag: tag, Fix: 2, Operator: []byte(fixture), CR: []byte(fixtureCR)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "version: 1.9.1+fix.2\n") {
		t.Errorf("Chart.yaml:\n%s", files["Chart.yaml"])
	}
}

func TestLayoutChanges(t *testing.T) {
	cases := []struct {
		name string
		op   string
		cr   string
		want string
	}{
		{"missing CRD", strings.Replace(fixture, "kind: CustomResourceDefinition", "kind: ClusterRole", 1), fixtureCR, "CustomResourceDefinition"},
		{"new kind", fixture + "---\napiVersion: v1\nkind: Service\nmetadata:\n  name: x\n  namespace: kubevirt\n", fixtureCR, "new kind Service"},
		{"no Deployment", strings.Replace(fixture, "kind: Deployment", "kind: ClusterRole", 1), fixtureCR, "Deployment"},
		{"renamed object", strings.Replace(fixture, "name: kubevirt-cluster-critical", "name: kubevirt-critical", 1), fixtureCR, "PriorityClass/kubevirt-cluster-critical"},
		{"other image registry", strings.Replace(fixture, "image: quay.io/kubevirt/virt-operator", "image: ghcr.io/kubevirt/virt-operator", 1), fixtureCR, "does not match"},
		{"version env missing", strings.Replace(fixture, "KUBEVIRT_VERSION", "OTHER_VERSION", 1), fixtureCR, "KUBEVIRT_VERSION"},
		{"untemplated version", strings.Replace(fixture, "value: 1000000000", "value: 1000000000\ndescription: built from v1.9.1", 1), fixtureCR, "did not template"},
		{"other namespace", strings.Replace(fixture, "  name: kubevirt-operator\n  namespace: kubevirt\n---\napiVersion: rbac.authorization.k8s.io/v1\nkind: Role", "  name: kubevirt-operator\n  namespace: other\n---\napiVersion: rbac.authorization.k8s.io/v1\nkind: Role", 1), fixtureCR, "namespace"},
		{"CR kind", fixture, strings.Replace(fixtureCR, "kind: KubeVirt", "kind: Other", 1), "want KubeVirt"},
		{"CR metadata", fixture, strings.Replace(fixtureCR, "  name: kubevirt\n", "  name: kubevirt\n  labels: {a: b}\n", 1), "metadata.labels"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := gen(t, c.op, c.cr)
			if err == nil {
				t.Fatalf("no error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

func TestBadTag(t *testing.T) {
	p, _ := upstream.Get("kubevirt")
	for _, tg := range []string{"v1.9.1-rc.0", "1.9.1", "v1.9"} {
		if _, err := Generate(Input{Project: p, Tag: tg, Operator: []byte(fixture), CR: []byte(fixtureCR)}); err == nil {
			t.Errorf("%s: no error", tg)
		}
	}
}

// TestFollowImageTag makes sure that only new kubevirt charts take over spec.imageTag.
// The versions in upstream.ChartsBeforeFollowImageTag are published, so they must not
// change.
func TestFollowImageTag(t *testing.T) {
	kv, _ := upstream.Get("kubevirt")
	if cdi, _ := upstream.Get("cdi"); cdi.FollowImageTag {
		t.Error("cdi has FollowImageTag, want only kubevirt")
	}
	const marker = `set $spec "imageTag" .Values.image.tag`
	for _, c := range []struct {
		name string
		p    *upstream.Project
		tag  string
		fix  int
		want bool
	}{
		{"published 1.8.2", kv, "v1.8.2", 0, false},
		{"published 1.9.1", kv, "v1.9.1", 0, false},
		{"published 1.10.0", kv, "v1.10.0", 0, false},
		{"fix of 1.9.1", kv, "v1.9.1", 1, true},
		{"new release", kv, "v1.11.0", 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			op := strings.ReplaceAll(fixture, tag, c.tag)
			cr := strings.ReplaceAll(fixtureCR, tag, c.tag)
			files, err := Generate(Input{Project: c.p, Tag: c.tag, Fix: c.fix, Operator: []byte(op), CR: []byte(cr)})
			if err != nil {
				t.Fatal(err)
			}
			tpl := string(files["templates/kubevirt.yaml"])
			if got := strings.Contains(tpl, marker); got != c.want {
				t.Errorf("imageTag block in templates/kubevirt.yaml = %v, want %v\n%s", got, c.want, tpl)
			}
			if got := strings.Contains(string(files["values.yaml"]), "spec.imageTag"); got != c.want {
				t.Errorf("imageTag comment in values.yaml = %v, want %v", got, c.want)
			}
		})
	}
}
