package upstream

import "testing"

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		tag  string
		fix  int
		want string
	}{
		{"v1.9.1", 0, "1.9.1"},
		{"v1.65.0", 3, "1.65.0+fix.3"},
	} {
		got, err := ChartVersion(c.tag, c.fix)
		if err != nil || got != c.want {
			t.Errorf("ChartVersion(%s, %d) = %q, %v", c.tag, c.fix, got, err)
		}
		tag, fix, err := ParseChartVersion(got)
		if err != nil || tag != c.tag || fix != c.fix {
			t.Errorf("ParseChartVersion(%s) = %s, %d, %v", got, tag, fix, err)
		}
	}
	for _, bad := range []string{"v1.9.1-rc.1", "v1.9.1-alpha.0", "v1.10.0-beta.2", "1.9.1", "v1.9"} {
		if IsStable(bad) {
			t.Errorf("IsStable(%s) = true", bad)
		}
	}
	if _, _, err := ParseChartVersion("1.9.1-1"); err == nil {
		t.Errorf("ParseChartVersion(1.9.1-1): no error")
	}
	if CompareTags("v1.9.10", "v1.9.9") != 1 || CompareTags("v1.10.0", "v1.9.9") != 1 || CompareTags("v1.9.1", "v1.9.1") != 0 {
		t.Errorf("CompareTags does not sort numerically")
	}
}

// TestProjects makes sure that each project sets the fields of its shape.
func TestProjects(t *testing.T) {
	for _, name := range Names() {
		p, err := Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != name || p.Repo == "" || !IsStable(p.MinTag) {
			t.Errorf("%s: name %q, repo %q, MinTag %q", name, p.Name, p.Repo, p.MinTag)
		}
		switch p.Shape {
		case ShapeOperator:
			if p.OperatorFile == "" || p.CRFile == "" || p.CRDName == "" || p.DeploymentName == "" || p.ImageRegistry == "" {
				t.Errorf("%s: operator fields are not set", name)
			}
		case ShapeCRDs:
			if p.SourceFile == "" || len(p.CRDNames) == 0 {
				t.Errorf("%s: SourceFile or CRDNames not set", name)
			}
			if p.OperatorFile != "" || p.CRFile != "" || p.DeploymentName != "" || len(p.ImageEnv) > 0 {
				t.Errorf("%s: a CRD-only project sets operator fields", name)
			}
		default:
			t.Errorf("%s: unknown shape %q", name, p.Shape)
		}
	}
	if len(Projects) != len(Names()) {
		t.Errorf("Names() has %d projects, Projects has %d", len(Names()), len(Projects))
	}
}

func TestURLs(t *testing.T) {
	if got, want := RepoFileURL("k8snetworkplumbingwg/multus-cni", "v4.3.2", "deployments/multus-daemonset.yml"),
		"https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/v4.3.2/deployments/multus-daemonset.yml"; got != want {
		t.Errorf("RepoFileURL = %s", got)
	}
	if got, want := ReleaseAssetURL("kubevirt/kubevirt", "v1.9.1", "kubevirt-cr.yaml"),
		"https://github.com/kubevirt/kubevirt/releases/download/v1.9.1/kubevirt-cr.yaml"; got != want {
		t.Errorf("ReleaseAssetURL = %s", got)
	}
}
