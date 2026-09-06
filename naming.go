package gobridge

import (
	"fmt"
	"maps"
	"strings"
)

// Names controls one language's public API. Keys use wire operation names,
// Go type names (optionally import/path.Type), and Type.json_field names.
// Renames never change the wire protocol. Unknown keys fail generation.
type Names struct {
	Class      string            `json:"class,omitempty"`
	Operations map[string]string `json:"operations,omitempty"`
	Types      map[string]string `json:"types,omitempty"`
	Fields     map[string]string `json:"fields,omitempty"`
}

// Option configures generated bindings. Options compose left to right; later
// entries override earlier entries without replacing unrelated map entries.
type Option func(*Registry)

func mergeNames(dst *Names, src Names) {
	if src.Class != "" {
		dst.Class = src.Class
	}
	merge := func(a *map[string]string, b map[string]string) {
		if len(b) == 0 {
			return
		}
		if *a == nil {
			*a = map[string]string{}
		}
		maps.Copy(*a, b)
	}
	merge(&dst.Operations, src.Operations)
	merge(&dst.Types, src.Types)
	merge(&dst.Fields, src.Fields)
}

// WithPython supplies Python class, operation, model and field names.
func WithPython(names Names) Option {
	var snapshot Names
	mergeNames(&snapshot, names)
	return func(r *Registry) { mergeNames(&r.pythonNames, snapshot) }
}

// WithTypeScript supplies TypeScript class, operation, model and field names.
func WithTypeScript(names Names) Option {
	var snapshot Names
	mergeNames(&snapshot, names)
	return func(r *Registry) { mergeNames(&r.typescriptNames, snapshot) }
}

func (f Field) publicName() string {
	if f.PublicName != "" {
		return f.PublicName
	}
	return f.Name
}
func (o Operation) publicName() string {
	if o.PublicName != "" {
		return o.PublicName
	}
	return o.Name
}
func tsFieldName(f Field) string {
	if f.PublicName != "" {
		return f.PublicName
	}
	return tsCamel(f.Name)
}
func tsOperationName(o Operation) string {
	if o.PublicName != "" {
		return o.PublicName
	}
	return tsCamel(o.Name)
}

func (r *Registry) bindingSchema(language, class string, options []Option) (Schema, string, error) {
	configuration := New(WithPython(r.pythonNames), WithTypeScript(r.typescriptNames))
	for _, option := range options {
		if option != nil {
			option(configuration)
		}
	}
	names := configuration.pythonNames
	if language == "typescript" {
		names = configuration.typescriptNames
	}
	if names.Class != "" {
		class = names.Class
	}
	schema := r.Schema()
	used := map[string]bool{}
	lookup := func(kind string, values map[string]string, keys ...string) string {
		result := ""
		for _, key := range keys {
			if value, ok := values[key]; ok {
				used[kind+":"+key] = true
				result = value
			}
		}
		return result
	}
	var visit func(*Type) error
	visit = func(t *Type) error {
		if t.Elem != nil {
			return visit(t.Elem)
		}
		if t.Kind != "struct" {
			return nil
		}
		original := t.Name
		if name := lookup("types", names.Types, original, t.goName); name != "" {
			t.Name = name
		}
		seen := map[string]bool{}
		for i := range t.Fields {
			f := &t.Fields[i]
			f.PublicName = f.pythonName
			if language == "typescript" {
				f.PublicName = f.typescriptName
			}
			if name := lookup("fields", names.Fields, original+"."+f.goName, original+"."+f.Name, t.goName+"."+f.goName, t.goName+"."+f.Name); name != "" {
				f.PublicName = name
			}
			public := f.publicName()
			if language == "typescript" {
				public = tsFieldName(*f)
			}
			if seen[public] && language == "python" {
				return fmt.Errorf("duplicate %s field %s.%s", language, t.Name, public)
			}
			seen[public] = true
			if language == "python" && (!typescriptIdentifier.MatchString(public) || pythonKeywords[public] || public == "_runtime" || strings.HasPrefix(public, "_bridge")) {
				return fmt.Errorf("invalid or reserved Python field %s.%s", t.Name, public)
			}
			if err := visit(&f.Type); err != nil {
				return err
			}
		}
		return nil
	}
	seen := map[string]bool{}
	for i := range schema.Operations {
		op := &schema.Operations[i]
		op.PublicName = lookup("operations", names.Operations, op.Name)
		public := op.publicName()
		if language == "typescript" {
			public = tsOperationName(*op)
		}
		if seen[public] && language == "python" {
			return schema, class, fmt.Errorf("duplicate %s operation %s", language, public)
		}
		seen[public] = true
		if language == "python" && (!typescriptIdentifier.MatchString(public) || pythonReserved[public] || strings.HasPrefix(public, "_bridge")) {
			return schema, class, fmt.Errorf("invalid or reserved Python operation %s", public)
		}
		if err := visit(&op.Input); err != nil {
			return schema, class, err
		}
		if err := visit(&op.Output); err != nil {
			return schema, class, err
		}
	}
	if schema.Constructor != nil {
		if err := visit(schema.Constructor); err != nil {
			return schema, class, err
		}
		if language == "python" {
			for _, field := range schema.Constructor.Fields {
				if field.publicName() == "command" {
					return schema, class, fmt.Errorf("constructor field command conflicts with Python runtime options")
				}
			}
		}
	}
	for kind, values := range map[string]map[string]string{"operations": names.Operations, "types": names.Types, "fields": names.Fields} {
		for key, value := range values {
			if !used[kind+":"+key] || value == "" {
				return schema, class, fmt.Errorf("unknown or empty %s rename %s.%s", language, kind, key)
			}
		}
	}
	return schema, class, nil
}

// WithCommandPrefix locates the bridge subcommand inside an existing binary.
// The runtime appends "serve"; generation commands use the same prefix.
func WithCommandPrefix(args ...string) Option {
	snapshot := append([]string(nil), args...)
	return func(r *Registry) { r.commandPrefix = append([]string(nil), snapshot...) }
}
func (r *Registry) generationPrefix(options []Option) []string {
	config := New(WithCommandPrefix(r.commandPrefix...))
	for _, option := range options {
		if option != nil {
			option(config)
		}
	}
	return config.commandPrefix
}
