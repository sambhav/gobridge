package gobridge

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// APISnapshot describes public names separately from the daemon fingerprint.
type APISnapshot struct {
	Version         int    `json:"version"`
	PythonClass     string `json:"python_class"`
	TypeScriptClass string `json:"typescript_class"`
	Python          Schema `json:"python"`
	TypeScript      Schema `json:"typescript"`
}

func (r *Registry) API(class string, options ...Option) (APISnapshot, error) {
	py, pc, err := r.bindingSchema("python", class, options)
	if err != nil {
		return APISnapshot{}, err
	}
	ts, tc, err := r.bindingSchema("typescript", class, options)
	if err != nil {
		return APISnapshot{}, err
	}
	if err := r.GeneratePython(io.Discard, class, "service", options...); err != nil {
		return APISnapshot{}, err
	}
	if err := r.GenerateTypeScript(io.Discard, class, "service", options...); err != nil {
		return APISnapshot{}, err
	}
	return APISnapshot{1, pc, tc, py, ts}, nil
}

// APIChange conservatively marks changes requiring consumer review as breaking.
type APIChange struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Breaking bool   `json:"breaking"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
}

// DiffAPI ignores wire fingerprints and compares both language APIs. New
// operations and documentation-only edits are safe; uncertain type/constraint
// changes are conservative, not a proof of semantic compatibility.
func DiffAPI(before, after APISnapshot) ([]APIChange, error) {
	if before.Version != 1 || after.Version != 1 {
		return nil, fmt.Errorf("unsupported API snapshot version")
	}
	normalize := func(value APISnapshot) map[string]any {
		data, _ := json.Marshal(value)
		var result map[string]any
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.UseNumber()
		_ = dec.Decode(&result)
		for _, lang := range []string{"python", "typescript"} {
			delete(result[lang].(map[string]any), "schema_hash")
		}
		return result
	}
	changes := []APIChange{}
	var visit func(string, any, any)
	visit = func(path string, a, b any) {
		if reflect.DeepEqual(a, b) {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			sorted := make([]string, 0, len(keys))
			for k := range keys {
				sorted = append(sorted, k)
			}
			sort.Strings(sorted)
			for _, k := range sorted {
				visit(path+"."+k, am[k], bm[k])
			}
			return
		}
		aa, aok := a.([]any)
		ba, bok := b.([]any)
		keyed := func(values []any) (map[string]any, bool) {
			result := map[string]any{}
			for _, v := range values {
				m, ok := v.(map[string]any)
				if !ok {
					return nil, false
				}
				name, ok := m["name"].(string)
				if !ok || result[name] != nil {
					return nil, false
				}
				result[name] = m
			}
			return result, true
		}
		if aok && bok {
			am, ok := keyed(aa)
			bm, ok2 := keyed(ba)
			if ok && ok2 {
				visit(path, am, bm)
				return
			}
		}
		kind := "changed"
		breaking := true
		if a == nil {
			kind = "added"
			if strings.Contains(path, ".operations.") && !strings.Contains(strings.SplitN(path, ".operations.", 2)[1], ".") {
				breaking = false
			}
		}
		if b == nil {
			kind = "removed"
		}
		if strings.HasSuffix(path, ".description") {
			breaking = false
		}
		changes = append(changes, APIChange{strings.TrimPrefix(path, "."), kind, breaking, a, b})
	}
	visit("", normalize(before), normalize(after))
	return changes, nil
}
