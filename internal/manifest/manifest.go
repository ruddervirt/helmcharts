// Package manifest parses multi-document Kubernetes YAML and compares objects.
package manifest

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// Object is one decoded Kubernetes object.
type Object = map[string]any

var docSeparator = regexp.MustCompile(`(?m)^---[ \t]*$`)

// Split decodes a multi-document YAML stream. Empty documents are skipped.
func Split(data []byte) ([]Object, error) {
	var out []Object
	for i, doc := range docSeparator.Split(string(data), -1) {
		if strings.TrimSpace(stripComments(doc)) == "" {
			continue
		}
		var obj Object
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			return nil, fmt.Errorf("document %d: %w", i, err)
		}
		if obj == nil {
			continue
		}
		if Kind(obj) == "" || Name(obj) == "" {
			return nil, fmt.Errorf("document %d: missing kind or metadata.name", i)
		}
		out = append(out, obj)
	}
	return out, nil
}

func stripComments(doc string) string {
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// Marshal encodes objects as a multi-document YAML stream.
func Marshal(objs []Object) ([]byte, error) {
	var buf bytes.Buffer
	for _, o := range objs {
		b, err := yaml.Marshal(o)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", Key(o), err)
		}
		buf.WriteString("---\n")
		buf.Write(b)
	}
	return buf.Bytes(), nil
}

// Kind returns the object kind.
func Kind(o Object) string { s, _ := o["kind"].(string); return s }

// APIVersion returns the object apiVersion.
func APIVersion(o Object) string { s, _ := o["apiVersion"].(string); return s }

// Metadata returns the metadata map, creating it if it is missing.
func Metadata(o Object) map[string]any {
	m, ok := o["metadata"].(map[string]any)
	if !ok {
		m = map[string]any{}
		o["metadata"] = m
	}
	return m
}

// Name returns metadata.name.
func Name(o Object) string { s, _ := Metadata(o)["name"].(string); return s }

// Namespace returns metadata.namespace.
func Namespace(o Object) string { s, _ := Metadata(o)["namespace"].(string); return s }

// Key identifies an object by kind, namespace, and name.
func Key(o Object) string {
	if ns := Namespace(o); ns != "" {
		return fmt.Sprintf("%s/%s/%s", Kind(o), ns, Name(o))
	}
	return fmt.Sprintf("%s/%s", Kind(o), Name(o))
}

// KindName identifies an object by kind and name only.
func KindName(o Object) string { return Kind(o) + "/" + Name(o) }

// Annotations returns metadata.annotations, or nil.
func Annotations(o Object) map[string]any {
	m, _ := Metadata(o)["annotations"].(map[string]any)
	return m
}

// SetAnnotation sets one annotation.
func SetAnnotation(o Object, key, value string) {
	md := Metadata(o)
	a, ok := md["annotations"].(map[string]any)
	if !ok {
		a = map[string]any{}
		md["annotations"] = a
	}
	a[key] = value
}

// RemoveAnnotations deletes the annotations whose key matches drop.
// It removes the annotations map if it becomes empty.
func RemoveAnnotations(o Object, drop func(string) bool) {
	md := Metadata(o)
	a, ok := md["annotations"].(map[string]any)
	if !ok {
		return
	}
	for k := range a {
		if drop(k) {
			delete(a, k)
		}
	}
	if len(a) == 0 {
		delete(md, "annotations")
	}
}

// IsHook reports whether the object is a Helm hook.
func IsHook(o Object) bool {
	_, ok := Annotations(o)["helm.sh/hook"]
	return ok
}

// PruneEmpty removes empty maps and empty lists recursively.
// Upstream CR files contain fields such as "featureGates: []" that have no effect.
func PruneEmpty(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			p := PruneEmpty(val)
			if isEmpty(p) {
				delete(t, k)
			} else {
				t[k] = p
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = PruneEmpty(t[i])
		}
		return t
	default:
		return v
	}
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

// WalkStrings calls fn for every string value in v with its path.
func WalkStrings(v any, path string, fn func(path, s string)) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			WalkStrings(t[k], path+"."+k, fn)
		}
	case []any:
		for i, e := range t {
			WalkStrings(e, fmt.Sprintf("%s[%d]", path, i), fn)
		}
	case string:
		fn(path, t)
	}
}

// Diff returns the paths where a and b differ. An empty result means equal.
func Diff(a, b any) []string {
	var out []string
	diff(a, b, "", &out)
	sort.Strings(out)
	return out
}

func diff(a, b any, path string, out *[]string) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		for k, av := range am {
			bv, ok := bm[k]
			if !ok {
				*out = append(*out, fmt.Sprintf("%s.%s: only in first", path, k))
				continue
			}
			diff(av, bv, path+"."+k, out)
		}
		for k := range bm {
			if _, ok := am[k]; !ok {
				*out = append(*out, fmt.Sprintf("%s.%s: only in second", path, k))
			}
		}
		return
	}
	al, aok := a.([]any)
	bl, bok := b.([]any)
	if aok && bok && len(al) == len(bl) {
		for i := range al {
			diff(al[i], bl[i], fmt.Sprintf("%s[%d]", path, i), out)
		}
		return
	}
	if !reflect.DeepEqual(a, b) {
		*out = append(*out, fmt.Sprintf("%s: %v != %v", path, short(a), short(b)))
	}
}

func short(v any) string {
	s := fmt.Sprintf("%v", v)
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}
