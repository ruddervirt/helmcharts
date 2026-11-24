// Package upstream describes the upstream projects and downloads their release files.
package upstream

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Shape is the kind of chart that the generator makes for a project.
type Shape string

const (
	// ShapeOperator is an operator manifest (one Namespace, one static CRD, one
	// Deployment, RBAC) plus a custom resource file, both release assets.
	ShapeOperator Shape = "operator"
	// ShapeCRDs is a set of CustomResourceDefinitions, picked by name from one
	// repository file. All other documents in the file are dropped.
	ShapeCRDs Shape = "crds"
)

// Project describes the layout of one upstream release that the generator expects.
type Project struct {
	// Name is the chart name. For ShapeOperator, it is also the values key of the custom resource.
	Name        string
	Description string
	Home        string
	Repo        string // GitHub owner/name
	Shape       Shape
	// MinTag is the oldest tag that the update workflow generates charts for.
	MinTag string

	// ShapeCRDs fields.

	// SourceFile is a repository file path at the release tag, for example
	// deployments/multus-daemonset.yml. It is downloaded from raw.githubusercontent.com.
	SourceFile string
	// CRDNames are the CRDs that the chart contains. A missing CRD, or a CRD in
	// SourceFile that is not listed, stops the generator.
	CRDNames []string

	// ShapeOperator fields.

	// OperatorFile and CRFile are release asset names.
	OperatorFile string
	CRFile       string
	// Namespace is the namespace that the upstream manifest uses.
	Namespace string
	// CRDName is the one static CRD in the operator manifest.
	CRDName string
	// CRKind and CRName identify the custom resource in CRFile.
	CRKind       string
	CRName       string
	CRNamespaced bool
	// DeploymentName is the operator Deployment.
	DeploymentName string
	// ImageRegistry is the registry and repository prefix of all upstream images.
	ImageRegistry string
	// VersionEnv are operator env vars whose value must equal the upstream tag.
	VersionEnv []string
	// ImageEnv are operator env vars whose value must be an upstream image.
	ImageEnv []string
	// Required lists Kind/name of objects that must exist in the operator manifest.
	// Helm adopts existing resources by name, so a rename must stop the generator.
	Required []string
	// ObservedVersionField is the status field of the CR with the running version.
	ObservedVersionField string
	// DeferUninstallStrategy makes the chart leave out spec.uninstallStrategy while the
	// live CR has none and runs the chart version. KubeVirt hashes the field into its
	// install strategy, so setting it rolls all components. The chart sets it at the
	// next version change, which rolls the components anyway.
	DeferUninstallStrategy bool
}

// AllowedKinds are the kinds that may appear in an operator manifest (ShapeOperator).
// A new kind stops the generator, because it needs a review.
var AllowedKinds = map[string]bool{
	"Namespace":                true,
	"CustomResourceDefinition": true,
	"PriorityClass":            true,
	"ClusterRole":              true,
	"ClusterRoleBinding":       true,
	"Role":                     true,
	"RoleBinding":              true,
	"ServiceAccount":           true,
	"Deployment":               true,
}

// Projects are the supported upstream projects.
var Projects = map[string]*Project{
	"kubevirt": {
		Name:           "kubevirt",
		Description:    "KubeVirt operator and KubeVirt custom resource, generated from the upstream release manifests",
		Home:           "https://kubevirt.io",
		Repo:           "kubevirt/kubevirt",
		Shape:          ShapeOperator,
		OperatorFile:   "kubevirt-operator.yaml",
		CRFile:         "kubevirt-cr.yaml",
		Namespace:      "kubevirt",
		CRDName:        "kubevirts.kubevirt.io",
		CRKind:         "KubeVirt",
		CRName:         "kubevirt",
		CRNamespaced:   true,
		DeploymentName: "virt-operator",
		ImageRegistry:  "quay.io/kubevirt",
		VersionEnv:     []string{"KUBEVIRT_VERSION"},
		ImageEnv:       []string{"VIRT_OPERATOR_IMAGE"},
		Required: []string{
			"Namespace/kubevirt",
			"CustomResourceDefinition/kubevirts.kubevirt.io",
			"PriorityClass/kubevirt-cluster-critical",
			"ClusterRole/kubevirt.io:operator",
			"ServiceAccount/kubevirt-operator",
			"Role/kubevirt-operator",
			"RoleBinding/kubevirt-operator-rolebinding",
			"ClusterRole/kubevirt-operator",
			"ClusterRoleBinding/kubevirt-operator",
			"Deployment/virt-operator",
		},
		MinTag:                 "v1.9.1",
		ObservedVersionField:   "observedKubeVirtVersion",
		DeferUninstallStrategy: true,
	},
	"cdi": {
		Name:           "cdi",
		Description:    "Containerized Data Importer operator and CDI custom resource, generated from the upstream release manifests",
		Home:           "https://github.com/kubevirt/containerized-data-importer",
		Repo:           "kubevirt/containerized-data-importer",
		Shape:          ShapeOperator,
		OperatorFile:   "cdi-operator.yaml",
		CRFile:         "cdi-cr.yaml",
		Namespace:      "cdi",
		CRDName:        "cdis.cdi.kubevirt.io",
		CRKind:         "CDI",
		CRName:         "cdi",
		CRNamespaced:   false,
		DeploymentName: "cdi-operator",
		ImageRegistry:  "quay.io/kubevirt",
		VersionEnv:     []string{"OPERATOR_VERSION"},
		ImageEnv: []string{
			"CONTROLLER_IMAGE", "IMPORTER_IMAGE", "CLONER_IMAGE",
			"APISERVER_IMAGE", "UPLOAD_SERVER_IMAGE", "UPLOAD_PROXY_IMAGE",
		},
		Required: []string{
			"Namespace/cdi",
			"CustomResourceDefinition/cdis.cdi.kubevirt.io",
			"ClusterRole/cdi-operator-cluster",
			"ClusterRoleBinding/cdi-operator",
			"ServiceAccount/cdi-operator",
			"Role/cdi-operator",
			"RoleBinding/cdi-operator",
			"Deployment/cdi-operator",
		},
		MinTag:               "v1.65.0",
		ObservedVersionField: "observedVersion",
	},
	"multus-crds": {
		Name:        "multus-crds",
		Description: "NetworkAttachmentDefinition CRD of Multus CNI, generated from the upstream deployment manifest",
		Home:        "https://github.com/k8snetworkplumbingwg/multus-cni",
		Repo:        "k8snetworkplumbingwg/multus-cni",
		Shape:       ShapeCRDs,
		SourceFile:  "deployments/multus-daemonset.yml",
		CRDNames:    []string{"network-attachment-definitions.k8s.cni.cncf.io"},
		// rke2-multus v4.3.017 has appVersion 4.3.0 and v4.3.118 has 4.3.1.
		// ruddervirt-setup supports both.
		MinTag: "v4.3.0",
	},
}

// Get returns the project with the given name.
func Get(name string) (*Project, error) {
	p, ok := Projects[name]
	if !ok {
		return nil, fmt.Errorf("unknown project %q (want one of %s)", name, strings.Join(Names(), ", "))
	}
	return p, nil
}

// Names returns the project names in a fixed order.
func Names() []string { return []string{"kubevirt", "cdi", "multus-crds"} }

var stableTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// IsStable reports whether tag is a stable release tag (vMAJOR.MINOR.PATCH, no suffix).
func IsStable(tag string) bool { return stableTag.MatchString(tag) }

// ChartVersion returns the chart version for a tag and fix number.
// The chart version is the tag without "v". A chart fix for the same tag adds "+fix.N".
func ChartVersion(tag string, fix int) (string, error) {
	if !IsStable(tag) {
		return "", fmt.Errorf("tag %q is not a stable vMAJOR.MINOR.PATCH tag", tag)
	}
	v := strings.TrimPrefix(tag, "v")
	if fix > 0 {
		v += "+fix." + strconv.Itoa(fix)
	}
	return v, nil
}

var fixSuffix = regexp.MustCompile(`^(\d+\.\d+\.\d+)(?:\+fix\.(\d+))?$`)

// ParseChartVersion splits a chart version into the upstream tag and the fix number.
func ParseChartVersion(v string) (tag string, fix int, err error) {
	m := fixSuffix.FindStringSubmatch(v)
	if m == nil {
		return "", 0, fmt.Errorf("chart version %q does not match X.Y.Z or X.Y.Z+fix.N", v)
	}
	if m[2] != "" {
		fix, _ = strconv.Atoi(m[2])
	}
	return "v" + m[1], fix, nil
}

// CompareTags compares two stable tags. It returns -1, 0, or 1.
func CompareTags(a, b string) int {
	pa, pb := parts(a), parts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parts(tag string) [3]int {
	m := stableTag.FindStringSubmatch(tag)
	var out [3]int
	if m == nil {
		return out
	}
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}

var client = &http.Client{Timeout: 2 * time.Minute}

func get(url string, auth bool) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ruddervirt-chartgen")
	if tok := os.Getenv("GITHUB_TOKEN"); auth && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Fetch downloads one release asset. If CHARTGEN_CACHE is set, it caches files there.
func (p *Project) Fetch(tag, file string) ([]byte, error) {
	return p.fetch(tag, file, ReleaseAssetURL(p.Repo, tag, file))
}

// FetchSource downloads SourceFile at the tag (ShapeCRDs).
func (p *Project) FetchSource(tag string) ([]byte, error) {
	if p.SourceFile == "" {
		return nil, fmt.Errorf("project %s has no SourceFile", p.Name)
	}
	return p.fetch(tag, "source/"+p.SourceFile, RepoFileURL(p.Repo, tag, p.SourceFile))
}

// ReleaseAssetURL is the download URL of a release asset.
func ReleaseAssetURL(repo, tag, file string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, file)
}

// RepoFileURL is the download URL of a repository file at a tag.
func RepoFileURL(repo, tag, path string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repo, tag, path)
}

func (p *Project) fetch(tag, cacheName, url string) ([]byte, error) {
	cache := os.Getenv("CHARTGEN_CACHE")
	var cached string
	if cache != "" {
		cached = filepath.Join(cache, p.Name, tag, filepath.FromSlash(cacheName))
		if b, err := os.ReadFile(cached); err == nil {
			return b, nil
		}
	}
	b, err := get(url, false)
	if err != nil {
		return nil, err
	}
	if cached != "" {
		if err := os.MkdirAll(filepath.Dir(cached), 0o755); err == nil {
			_ = os.WriteFile(cached, b, 0o644)
		}
	}
	return b, nil
}

// StableTags lists the published, non-prerelease releases with a stable tag, oldest first.
func (p *Project) StableTags() ([]string, error) {
	var tags []string
	for page := 1; page <= 20; page++ {
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100&page=%d", p.Repo, page)
		b, err := get(url, true)
		if err != nil {
			return nil, err
		}
		var rels []struct {
			TagName    string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err := json.Unmarshal(b, &rels); err != nil {
			return nil, fmt.Errorf("decode releases: %w", err)
		}
		if len(rels) == 0 {
			break
		}
		for _, r := range rels {
			if !r.Draft && !r.Prerelease && IsStable(r.TagName) {
				tags = append(tags, r.TagName)
			}
		}
	}
	sort.Slice(tags, func(i, j int) bool { return CompareTags(tags[i], tags[j]) < 0 })
	return tags, nil
}
