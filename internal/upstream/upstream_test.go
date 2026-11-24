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
