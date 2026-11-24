// Package diff compares the rendered charts with the upstream release manifests.
//
// For every chart under charts/, it renders the chart with default values and makes
// sure that each resource equals the upstream resource. For an operator chart, the
// only allowed differences are the removed Namespace, Helm annotations, the hooks,
// and the custom resource. For a CRD-only chart, the only allowed difference is the
// keep annotation.
package diff

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/ruddervirt/kubevirt/internal/chartgen"
	"github.com/ruddervirt/kubevirt/internal/manifest"
	"github.com/ruddervirt/kubevirt/internal/upstream"
)

var root = filepath.Join("..", "..")

type chart struct {
	dir     string
	project *upstream.Project
	tag     string
}

func charts(t *testing.T) []chart {
	t.Helper()
	var out []chart
	for _, name := range upstream.Names() {
		p, _ := upstream.Get(name)
		matches, err := filepath.Glob(filepath.Join(root, "charts", name, "*", "Chart.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			var c struct {
				Name       string `json:"name"`
				Version    string `json:"version"`
				AppVersion string `json:"appVersion"`
			}
			if err := yaml.Unmarshal(b, &c); err != nil {
				t.Fatal(err)
			}
			tag, _, err := upstream.ParseChartVersion(c.Version)
			if err != nil {
				t.Fatal(err)
			}
			if c.AppVersion != tag {
				t.Errorf("%s: appVersion %q, want %q", m, c.AppVersion, tag)
			}
			out = append(out, chart{dir: filepath.Dir(m), project: p, tag: tag})
		}
	}
	if len(out) == 0 {
		t.Fatal("no charts found")
	}
	return out
}

func helm(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("helm", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out
}

func render(t *testing.T, c chart, namespace string, extra ...string) []manifest.Object {
	t.Helper()
	if namespace == "" {
		namespace = "kube-system" // a CRD-only project has no upstream namespace
	}
	args := append([]string{"template", c.project.Name, c.dir, "--namespace", namespace, "--include-crds"}, extra...)
	objs, err := manifest.Split(helm(t, args...))
	if err != nil {
		t.Fatal(err)
	}
	return objs
}

func fetch(t *testing.T, c chart, file string) []byte {
	t.Helper()
	b, err := c.project.Fetch(c.tag, file)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLint(t *testing.T) {
	for _, c := range charts(t) {
		t.Run(c.dir, func(t *testing.T) {
			helm(t, "lint", "--strict", c.dir)
		})
	}
}

func TestMatchesUpstream(t *testing.T) {
	for _, c := range charts(t) {
		if c.project.Shape == upstream.ShapeCRDs {
			t.Run(c.dir, func(t *testing.T) { matchCRDs(t, c) })
			continue
		}
		t.Run(c.dir, func(t *testing.T) {
			up, err := manifest.Split(fetch(t, c, c.project.OperatorFile))
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]manifest.Object{}
			for _, o := range up {
				want[manifest.Key(o)] = o
			}
			expectedCR, err := chartgen.ExpectedCR(c.project, fetch(t, c, c.project.CRFile), c.project.Namespace)
			if err != nil {
				t.Fatal(err)
			}

			matched := map[string]bool{}
			hooks := 0
			sawCR := false
			for _, o := range render(t, c, c.project.Namespace) {
				if manifest.IsHook(o) {
					hooks++
					continue
				}
				key := manifest.Key(o)
				if manifest.Kind(o) == "CustomResourceDefinition" {
					// Files in crds/ are not templates and carry no Helm annotations.
				} else if got := manifest.Annotations(o)[chartgen.KeepAnnotation]; got != "keep" {
					t.Errorf("%s: annotation %s = %v, want keep", key, chartgen.KeepAnnotation, got)
				}
				manifest.RemoveAnnotations(o, func(k string) bool { return k == chartgen.KeepAnnotation })
				if manifest.Kind(o) == c.project.CRKind {
					sawCR = true
					if d := manifest.Diff(o, expectedCR); len(d) > 0 {
						t.Errorf("custom resource differs from upstream %s defaults:\n  %s", c.project.CRFile, strings.Join(d, "\n  "))
					}
					continue
				}
				u, ok := want[key]
				if !ok {
					t.Errorf("%s: not in upstream %s", key, c.project.OperatorFile)
					continue
				}
				matched[key] = true
				if d := manifest.Diff(o, u); len(d) > 0 {
					t.Errorf("%s differs from upstream:\n  %s", key, strings.Join(d, "\n  "))
				}
			}
			for key := range want {
				if matched[key] {
					continue
				}
				if strings.HasPrefix(key, "Namespace/") {
					continue // removed on purpose
				}
				t.Errorf("%s: in upstream but not rendered", key)
			}
			if !sawCR {
				t.Errorf("custom resource %s not rendered", c.project.CRKind)
			}
			if hooks == 0 {
				t.Errorf("no hooks rendered")
			}
		})
	}
}

// matchCRDs makes sure that a CRD-only chart renders exactly the listed upstream
// CRDs, each with the keep annotation and otherwise equal to the upstream document.
func matchCRDs(t *testing.T, c chart) {
	src, err := c.project.FetchSource(c.tag)
	if err != nil {
		t.Fatal(err)
	}
	up, err := manifest.Split(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]manifest.Object{}
	listed := map[string]bool{}
	for _, n := range c.project.CRDNames {
		listed[n] = true
	}
	for _, o := range up {
		if manifest.Kind(o) == "CustomResourceDefinition" && listed[manifest.Name(o)] {
			want[manifest.Name(o)] = o
		}
	}
	if len(want) != len(listed) {
		t.Fatalf("upstream %s has %d of the %d listed CRDs", c.project.SourceFile, len(want), len(listed))
	}
	got := map[string]bool{}
	for _, o := range render(t, c, "chart-test-ns") {
		key := manifest.Key(o)
		if manifest.IsHook(o) {
			t.Errorf("%s: a CRD-only chart has no hooks", key)
			continue
		}
		if manifest.Kind(o) != "CustomResourceDefinition" {
			t.Errorf("%s: a CRD-only chart renders only CRDs", key)
			continue
		}
		u, ok := want[manifest.Name(o)]
		if !ok {
			t.Errorf("%s: not listed in CRDNames", key)
			continue
		}
		got[manifest.Name(o)] = true
		if v := manifest.Annotations(o)[chartgen.KeepAnnotation]; v != "keep" {
			t.Errorf("%s: annotation %s = %v, want keep", key, chartgen.KeepAnnotation, v)
		}
		manifest.RemoveAnnotations(o, func(k string) bool { return k == chartgen.KeepAnnotation })
		if d := manifest.Diff(o, u); len(d) > 0 {
			t.Errorf("%s differs from upstream:\n  %s", key, strings.Join(d, "\n  "))
		}
	}
	for n := range want {
		if !got[n] {
			t.Errorf("CRD %s: in upstream but not rendered", n)
		}
	}
	// "helm template --include-crds" renders files in crds/ too. A CRD-only chart has
	// none, because Helm never upgrades files in crds/.
	if _, err := os.Stat(filepath.Join(c.dir, "crds")); err == nil {
		t.Errorf("%s has a crds/ directory", c.dir)
	}
}

// TestOtherNamespace makes sure that no rendered resource keeps the upstream namespace.
func TestOtherNamespace(t *testing.T) {
	const ns = "chart-test-ns"
	for _, c := range charts(t) {
		t.Run(c.dir, func(t *testing.T) {
			for _, o := range render(t, c, ns) {
				key := manifest.Key(o)
				if got := manifest.Namespace(o); got != "" && got != ns {
					t.Errorf("%s: namespace %q", key, got)
				}
				if manifest.Kind(o) == "CustomResourceDefinition" {
					continue // descriptions mention the upstream namespace in examples
				}
				manifest.WalkStrings(o, key, func(path, s string) {
					if s == c.project.Namespace && strings.HasSuffix(strings.ToLower(path), "namespace") {
						t.Errorf("%s = %q", path, s)
					}
				})
			}
		})
	}
}

// TestNoDeleteHooks makes sure that the charts ship no hook that runs on uninstall.
func TestNoDeleteHooks(t *testing.T) {
	for _, c := range charts(t) {
		t.Run(c.dir, func(t *testing.T) {
			for _, o := range render(t, c, c.project.Namespace) {
				h, _ := manifest.Annotations(o)["helm.sh/hook"].(string)
				for _, typ := range strings.Split(h, ",") {
					switch strings.TrimSpace(typ) {
					case "", "pre-install", "pre-upgrade":
					default:
						t.Errorf("%s: hook type %q is not allowed", manifest.Key(o), typ)
					}
				}
			}
		})
	}
}

// TestValuesOverride makes sure that image values reach the Deployment and that
// unset custom resource fields do not render.
func TestValuesOverride(t *testing.T) {
	for _, c := range charts(t) {
		if c.project.Shape != upstream.ShapeOperator {
			continue // no images and no custom resource
		}
		t.Run(c.dir, func(t *testing.T) {
			objs := render(t, c, c.project.Namespace,
				"--set", "image.registry=registry.example.com/kv",
				"--set", "image.tag=v9.9.9",
				"--set", c.project.Name+".spec=null")
			for _, o := range objs {
				if manifest.Kind(o) == c.project.CRKind {
					if _, ok := o["spec"]; ok {
						t.Errorf("spec renders although the value is null: %v", o["spec"])
					}
				}
				if manifest.Kind(o) != "Deployment" {
					continue
				}
				manifest.WalkStrings(o, "Deployment", func(path, s string) {
					if strings.Contains(s, c.tag) || strings.Contains(s, "quay.io/kubevirt") {
						t.Errorf("%s = %q still has the upstream image or version", path, s)
					}
				})
			}
		})
	}
}

// TestExamples makes sure that each example HelmChart names an existing chart
// version, sets takeOwnership and failurePolicy, and that its values render.
func TestExamples(t *testing.T) {
	for _, name := range upstream.Names() {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, "examples", "helmchart-"+name+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var hc struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
				Metadata   struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Chart           string `json:"chart"`
					Version         string `json:"version"`
					TargetNamespace string `json:"targetNamespace"`
					CreateNamespace bool   `json:"createNamespace"`
					TakeOwnership   bool   `json:"takeOwnership"`
					FailurePolicy   string `json:"failurePolicy"`
					ValuesContent   string `json:"valuesContent"`
				} `json:"spec"`
			}
			// UnmarshalStrict fails on fields that the struct does not know.
			if err := yaml.UnmarshalStrict(b, &hc); err != nil {
				t.Fatal(err)
			}
			if hc.APIVersion != "helm.cattle.io/v1" || hc.Kind != "HelmChart" {
				t.Errorf("not a helm.cattle.io/v1 HelmChart")
			}
			if hc.Spec.Chart != "oci://ghcr.io/ruddervirt/charts/"+name {
				t.Errorf("chart = %q", hc.Spec.Chart)
			}
			if !hc.Spec.TakeOwnership || hc.Spec.FailurePolicy != "abort" {
				t.Errorf("takeOwnership = %v, failurePolicy = %q", hc.Spec.TakeOwnership, hc.Spec.FailurePolicy)
			}
			dir := filepath.Join(root, "charts", name, hc.Spec.Version)
			if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err != nil {
				t.Fatalf("chart version %s does not exist: %v", hc.Spec.Version, err)
			}
			values := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(values, []byte(hc.Spec.ValuesContent), 0o644); err != nil {
				t.Fatal(err)
			}
			helm(t, "template", hc.Metadata.Name, dir, "--namespace", hc.Spec.TargetNamespace, "--values", values)
		})
	}
}
