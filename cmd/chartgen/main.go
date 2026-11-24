// Command chartgen generates Helm charts from upstream releases.
// The projects are in internal/upstream.
//
// Usage:
//
//	chartgen generate   --project kubevirt --tag v1.9.1 [--fix N] [--charts-dir charts]
//	chartgen regenerate [--charts-dir charts]
//	chartgen tags       --project kubevirt [--all]
//	chartgen previous   --project kubevirt --tag v1.9.1
//	chartgen missing    [--charts-dir charts]
//	chartgen latest     --project kubevirt [--charts-dir charts]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"sigs.k8s.io/yaml"

	"github.com/ruddervirt/kubevirt/internal/chartgen"
	"github.com/ruddervirt/kubevirt/internal/upstream"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "generate":
		err = cmdGenerate(os.Args[2:])
	case "regenerate":
		err = cmdRegenerate(os.Args[2:])
	case "tags":
		err = cmdTags(os.Args[2:])
	case "previous":
		err = cmdPrevious(os.Args[2:])
	case "missing":
		err = cmdMissing(os.Args[2:])
	case "latest":
		err = cmdLatest(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "chartgen:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: chartgen generate|regenerate|tags|previous|missing|latest [flags]")
	os.Exit(2)
}

// ChartDir returns the directory of one chart version.
func chartDir(chartsDir, project, version string) string {
	return filepath.Join(chartsDir, project, version)
}

func generate(chartsDir string, p *upstream.Project, tag string, fix int) (string, error) {
	version, err := upstream.ChartVersion(tag, fix)
	if err != nil {
		return "", err
	}
	in := chartgen.Input{Project: p, Tag: tag, Fix: fix}
	switch p.Shape {
	case upstream.ShapeCRDs:
		if in.Source, err = p.FetchSource(tag); err != nil {
			return "", err
		}
	default:
		if in.Operator, err = p.Fetch(tag, p.OperatorFile); err != nil {
			return "", err
		}
		if in.CR, err = p.Fetch(tag, p.CRFile); err != nil {
			return "", err
		}
	}
	files, err := chartgen.Generate(in)
	if err != nil {
		return "", err
	}
	dir := chartDir(chartsDir, p.Name, version)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func cmdGenerate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	tag := fs.String("tag", "", "upstream release tag, for example v1.9.1")
	fix := fs.Int("fix", 0, "chart fix number for the same upstream tag (version X.Y.Z+fix.N)")
	chartsDir := fs.String("charts-dir", "charts", "output root")
	_ = fs.Parse(args)
	p, err := upstream.Get(*project)
	if err != nil {
		return err
	}
	dir, err := generate(*chartsDir, p, *tag, *fix)
	if err != nil {
		return err
	}
	fmt.Println(dir)
	return nil
}

type chartMeta struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// existingCharts returns the chart versions under chartsDir/<project>.
func existingCharts(chartsDir, project string) ([]chartMeta, error) {
	matches, err := filepath.Glob(filepath.Join(chartsDir, project, "*", "Chart.yaml"))
	if err != nil {
		return nil, err
	}
	var out []chartMeta
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		var c chartMeta
		if err := yaml.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", m, err)
		}
		if c.Name != project || filepath.Base(filepath.Dir(m)) != c.Version {
			return nil, fmt.Errorf("%s: name %q version %q do not match the directory", m, c.Name, c.Version)
		}
		out = append(out, c)
	}
	return out, nil
}

func cmdRegenerate(args []string) error {
	fs := flag.NewFlagSet("regenerate", flag.ExitOnError)
	chartsDir := fs.String("charts-dir", "charts", "chart root")
	_ = fs.Parse(args)
	for _, name := range upstream.Names() {
		charts, err := existingCharts(*chartsDir, name)
		if err != nil {
			return err
		}
		p, _ := upstream.Get(name)
		for _, c := range charts {
			tag, fix, err := upstream.ParseChartVersion(c.Version)
			if err != nil {
				return err
			}
			dir, err := generate(*chartsDir, p, tag, fix)
			if err != nil {
				return err
			}
			fmt.Println(dir)
		}
	}
	return nil
}

func cmdTags(args []string) error {
	fs := flag.NewFlagSet("tags", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	all := fs.Bool("all", false, "also list tags older than the minimum supported tag")
	_ = fs.Parse(args)
	p, err := upstream.Get(*project)
	if err != nil {
		return err
	}
	tags, err := p.StableTags()
	if err != nil {
		return err
	}
	for _, t := range tags {
		if *all || upstream.CompareTags(t, p.MinTag) >= 0 {
			fmt.Println(t)
		}
	}
	return nil
}

func cmdPrevious(args []string) error {
	fs := flag.NewFlagSet("previous", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	tag := fs.String("tag", "", "upstream tag")
	_ = fs.Parse(args)
	p, err := upstream.Get(*project)
	if err != nil {
		return err
	}
	tags, err := p.StableTags()
	if err != nil {
		return err
	}
	prev := ""
	for _, t := range tags {
		if upstream.CompareTags(t, *tag) < 0 {
			prev = t
		}
	}
	if prev == "" {
		return fmt.Errorf("no stable %s release before %s", p.Name, *tag)
	}
	fmt.Println(prev)
	return nil
}

type missingTag struct {
	Project string `json:"project"`
	Tag     string `json:"tag"`
	Repo    string `json:"repo"` // GitHub owner/name of the upstream project
}

func cmdMissing(args []string) error {
	fs := flag.NewFlagSet("missing", flag.ExitOnError)
	chartsDir := fs.String("charts-dir", "charts", "chart root")
	_ = fs.Parse(args)
	out := []missingTag{}
	for _, name := range upstream.Names() {
		p, _ := upstream.Get(name)
		charts, err := existingCharts(*chartsDir, name)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, c := range charts {
			tag, _, err := upstream.ParseChartVersion(c.Version)
			if err != nil {
				return err
			}
			have[tag] = true
		}
		tags, err := p.StableTags()
		if err != nil {
			return err
		}
		for _, t := range tags {
			if upstream.CompareTags(t, p.MinTag) >= 0 && !have[t] {
				out = append(out, missingTag{Project: name, Tag: t, Repo: p.Repo})
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

// cmdLatest prints the directory of the newest chart version of a project.
func cmdLatest(args []string) error {
	fs := flag.NewFlagSet("latest", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	chartsDir := fs.String("charts-dir", "charts", "chart root")
	_ = fs.Parse(args)
	charts, err := existingCharts(*chartsDir, *project)
	if err != nil {
		return err
	}
	if len(charts) == 0 {
		return fmt.Errorf("no charts for %s in %s", *project, *chartsDir)
	}
	sort.Slice(charts, func(i, j int) bool {
		ti, fi, _ := upstream.ParseChartVersion(charts[i].Version)
		tj, fj, _ := upstream.ParseChartVersion(charts[j].Version)
		if c := upstream.CompareTags(ti, tj); c != 0 {
			return c < 0
		}
		return fi < fj
	})
	fmt.Println(chartDir(*chartsDir, *project, charts[len(charts)-1].Version))
	return nil
}
