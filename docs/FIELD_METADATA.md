# Shared field documentation and validation

Status: implemented in the draft PR after this design was published. This increment adds field
documentation and a small, strict set of validation constraints to the existing
Go wire types. Defaults and new omission or null behavior are separate work.

## One declaration for every entry point

Put documentation and constraints next to a field's JSON name:

```go
type GreetingOptions struct {
    Prefix string `json:"prefix" doc:"Text placed before each name." validate:"minlen=1,maxlen=80"`
    Limit  int    `json:"limit" doc:"Maximum names processed in one call." validate:"min=1,max=120"`
}

type GreetingRequest struct {
    Name string  `json:"name" doc:"Name to greet." validate:"minlen=1,maxlen=80"`
    Age  *int    `json:"age,omitempty" doc:"Optional age in years." validate:"min=0,max=120"`
}
```

The Go daemon enforces these rules for CLI calls, generated Python calls, and
direct `Registry.Call` calls. Constructor options use the same validation as
ordinary operation arguments. Nested structs are validated recursively.

`Register` input structs and structs passed as `Bind` parameters use identical
rules. A parameter such as `func(name string)` has no Go struct field on which
to place tags; use a named request struct when that parameter needs metadata.
Source generation preserves the tagged types when emitting registrations.

## Supported tags and rules

| Declaration | Applies to | Meaning |
| --- | --- | --- |
| `doc:"..."` | Every supported struct field | User-facing field documentation. |
| `validate:"min=0"` | Signed integer or floating-point fields | Inclusive numeric minimum. |
| `validate:"max=120"` | Signed integer or floating-point fields | Inclusive numeric maximum. |
| `validate:"minlen=1"` | String, slice, or map fields | Inclusive minimum length. |
| `validate:"maxlen=80"` | String, slice, or map fields | Inclusive maximum length. |

Rules may be combined with commas. Whitespace around each rule and its value
is ignored. Repeating a rule is an error, even when the repeated value matches.
An absent or empty `validate` tag adds no constraints.

String length counts Unicode code points, matching Python's `len(str)` for
ordinary Unicode strings; it does not count UTF-8 bytes or visual grapheme
clusters. Slice length counts elements, and map length counts keys. A length
constraint applies to the container itself. Nested struct field constraints
continue to apply to elements and map values that are structs.

Pointers inherit their element's applicable rules. A nil pointer remains
optional and nullable. Null slices and maps retain their existing nullable
behavior. Constraints run on non-null values; `minlen=1` does not turn a
nullable field into a non-nullable one. Existing required-field and strict
wire-type checks run independently.

## Invalid declarations fail during registration

Registration rejects unknown rules, missing values, empty comma entries,
duplicate rules, malformed bounds, negative lengths, and incompatible rule
combinations. Examples include:

```go
Name  string `json:"name" validate:"min=1"`          // Numeric rule on a string.
Count int    `json:"count" validate:"minlen=1"`      // Length rule on a number.
Age   int    `json:"age" validate:"min=120,max=0"`   // Reversed bounds.
Name  string `json:"name" validate:"maxlen=-1"`      // Negative length.
Name  string `json:"name" validate:"required"`       // Unknown rule; no implicit defaults.
```

Integer fields require decimal integer bounds. Floating-point fields accept
finite decimal or scientific-notation bounds. Bounds must fit the field's
numeric type. NaN and infinity are rejected. Numeric schemas preserve exact
integer literals, including values outside float64's exact integer range;
registration and runtime checks must not silently round int64 limits.

Length limits are nonnegative integers no larger than 2,147,483,647, keeping
declarations stable across supported host architectures. For paired rules,
`min <= max` and `minlen <= maxlen` are required.

All validation plans are prepared when operations and constructors are
registered. Calls should compare already parsed constraints and avoid parsing
tags or converting bounds through arbitrary-precision numbers on each request.

## Schema contract

The field schema adds optional `description` and `constraints` members:

```json
{
  "name": "age",
  "type": {"kind": "ptr", "elem": {"kind": "int"}},
  "description": "Optional age in years.",
  "constraints": {"minimum": 0, "maximum": 120}
}
```

Length rules use `min_length` and `max_length` in `constraints`. Unspecified
members are omitted. Fields without either tag retain their existing schema
shape. Schema hashes include documentation and constraints so generated
bindings cannot silently disagree with the daemon's declaration.

The Go schema exposes field descriptions and a constraints struct rather than
an opaque string map. Numeric limits are serialized as JSON numbers with exact
integer values, for example through `json.Number`; the prepared validator keeps
the corresponding typed comparison values.

## CLI and Python experience

Operation help and constructor configuration help display field descriptions,
types, optionality, and declared limits. Help comes from the same schema as
Python generation. Invalid values return the existing `invalid_argument` error
with the field path and failed bound, such as:

```text
age: must be at most 120
request.name: length must be at least 1
```

Generated Python method signatures and return types remain ordinary Python
types. Generated dataclasses expose metadata through the standard dataclass
interface:

```python
from dataclasses import fields

age = next(field for field in fields(GreetingRequest) if field.name == "age")
assert age.metadata["description"] == "Optional age in years."
assert age.metadata["constraints"] == {"minimum": 0, "maximum": 120}
```

This metadata does not add a required validation framework. Dataclass
construction stays lightweight, and the Go daemon remains the enforcement
point. A Python caller receives the existing `InvalidArgumentError` when a
request violates a declared rule. Downstream tools can inspect field metadata
to build richer forms, editor hints, or optional client-side checks.

This increment does not infer defaults, coerce strings to numbers, add
Pydantic, make nullable containers non-nullable, or validate arbitrary Go
receiver internals. Constraints describe incoming wire fields; validating
outgoing results is separate work.

## Verification

Tests cover registration failures, inclusive numeric boundaries, exact int64
limits, Unicode lengths, slice and map lengths, nullable values, nested field
paths, and equivalent enforcement through `Register`, `Bind`, and constructor
initialization. CLI help and Python dataclass metadata receive integration
checks. Existing untagged schemas and valid requests retain their behavior.
