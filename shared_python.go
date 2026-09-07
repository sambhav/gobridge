package gobridge

import (
	"fmt"
	"strings"
)

func pySharedParams(b *strings.Builder, fields []Field, config ...string) {
	if len(config) != 0 {
		fmt.Fprintf(b, "{\"config\": %s, \"params\": ", config[0])
	}
	pyParams(b, fields)
	if len(config) != 0 {
		fmt.Fprint(b, "}")
	}
}

func pyShared(b *strings.Builder, s Schema, class string) {
	// Call already provides the runtime's wire encoding and immutable snapshot.
	fmt.Fprint(b, "\nclass _bridge_SharedCalls:\n    def __init__(self, config):\n        self._bridge_config = config\n\n")
	for _, op := range s.Operations {
		if !op.Shared || op.Stream {
			continue
		}
		fmt.Fprintf(b, "    def %s(self", op.publicName())
		if len(op.Input.Fields) > 0 {
			fmt.Fprint(b, ", *")
			pyFields(b, op.Input.Fields)
		}
		fmt.Fprintf(b, ") -> _bridge_Call[%s]:\n        return _bridge_Call(%q, ", pyType(op.Output), op.Name)
		pySharedParams(b, op.Input.Fields, "self._bridge_config")
		fmt.Fprintf(b, ", lambda value: _bridge_decode(%s, value))\n\n", pyDecodeType(op.Output))
	}
	for _, async := range []bool{false, true} {
		name, prefix, call := "Sync"+class, "", "self._bridge_client().call"
		if async {
			name, prefix, call = class, "async ", "await self._bridge_client().acall"
		}
		fmt.Fprintf(b, "class %s:\n    \"\"\"Immutable configuration using the module default or active session.\"\"\"\n    def __init__(self, *", name)
		pyFields(b, s.SharedConstructor.Fields)
		fmt.Fprint(b, ", _client: Client | None = None):\n        self._bridge_transport = _client\n        self._bridge_config = _bridge_Call(\"$config\", ")
		pyParams(b, s.SharedConstructor.Fields)
		fmt.Fprint(b, ", lambda value: value).wire()[\"params\"]\n        self.calls = _bridge_SharedCalls(self._bridge_config)\n\n    def _bridge_client(self) -> Client:\n        return self._bridge_transport if self._bridge_transport is not None else _bridge_defaults.client()\n\n")
		for _, op := range s.Operations {
			if op.Shared {
				pyMethod(b, op, op.publicName(), "    ", prefix, "self, ", call, "self._bridge_config")
			}
		}
	}
}
