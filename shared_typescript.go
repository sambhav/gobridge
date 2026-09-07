package gobridge

import (
	"fmt"
	"strings"
)

func tsShared(b *strings.Builder, s Schema, class string) {
	fmt.Fprintf(b, "\nexport interface %sOptions {\n", class)
	tsFields(b, s.SharedConstructor.Fields)
	fmt.Fprint(b, "  /** Pin this object to an explicit transport; otherwise use the active session or module default. */\n  readonly _client?: _bridgeClient;\n}\n\n")
	fmt.Fprintf(b, "export class %s {\n  readonly #config: unknown;\n  readonly #client?: _bridgeClient;\n", class)
	def := " = {}"
	if tsRequired(s.SharedConstructor.Fields) {
		def = ""
	}
	fmt.Fprintf(b, "  constructor(options: %sOptions%s) {\n", class, def)
	fmt.Fprint(b, "    const { _client, ...config } = options;\n    this.#client = _client;\n    this.#config = structuredClone(_bridgeEncode(schema.shared_constructor!, config));\n  }\n\n  readonly calls = {\n")
	for index, op := range s.Operations {
		if !op.Shared || op.Stream {
			continue
		}
		params, input := "", "{}"
		if len(op.Input.Fields) > 0 {
			params, input = "params: "+op.Input.Name, "params"
		}
		fmt.Fprintf(b, "    %s: (%s): _bridgeCall<%s> => new _bridgeCall(%s, { config: this.#config, params: _bridgeInput%d.encode(%s) }, value => _bridgeOutput%d.decode(value)),\n", tsOperationName(op), params, tsType(op.Output), tsQuote(op.Name), index, input, index)
	}
	fmt.Fprint(b, "  };\n\n")
	for index, op := range s.Operations {
		if !op.Shared {
			continue
		}
		tsJSDoc(b, "  ", op.Description, nil)
		params, input := "options?: _bridgeCallOptions", "{}"
		if len(op.Input.Fields) > 0 {
			params, input = "params: "+op.Input.Name+", "+params, "params"
		}
		wire := fmt.Sprintf("{ config: this.#config, params: _bridgeInput%d.encode(%s) }", index, input)
		if op.Stream {
			fmt.Fprintf(b, "  async *%s(%s): AsyncGenerator<%s> {\n    for await (const item of (this.#client ?? _bridgeDefaults.client()).stream(%s, %s, options)) { yield _bridgeOutput%d.decode(item); }\n  }\n\n", tsOperationName(op), params, tsType(op.Output), tsQuote(op.Name), wire, index)
		} else {
			fmt.Fprintf(b, "  async %s(%s): Promise<%s> {\n    const result = await (this.#client ?? _bridgeDefaults.client()).call(%s, %s, options);\n    return _bridgeOutput%d.decode(result);\n  }\n\n", tsOperationName(op), params, tsType(op.Output), tsQuote(op.Name), wire, index)
		}
	}
	fmt.Fprint(b, "}\n")
}
