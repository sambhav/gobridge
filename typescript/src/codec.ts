import { DaemonError, InvalidArgumentError } from "./errors.js";
import type { Schema, WireType } from "./types.js";

const integerText = /^-?(?:0|[1-9]\d*)$/;
const int64Min = -(1n << 63n);
const int64Max = (1n << 63n) - 1n;

/** Preserve unsafe integer tokens before Number rounding loses information. */
export function parseWire(text: string): unknown {
  return JSON.parse(text, (_key: string, value: unknown, context?: { source: string }) => {
    if (typeof value === "number") {
      if (!Number.isFinite(value)) throw new DaemonError("protocol", "non-finite JSON number");
      if (Number.isInteger(value) && !Number.isSafeInteger(value)) {
        if (context === undefined) throw new DaemonError("runtime", "Node 24 or newer is required for exact JSON numbers");
        if (integerText.test(context.source)) return BigInt(context.source);
      }
    }
    return value;
  });
}

/** Emit bigint as a JSON number, without changing the Go/Python protocol. */
export function stringifyWire(value: unknown): string {
  const result = JSON.stringify(value, (_key, item: unknown) => {
    if (typeof item === "bigint") {
      return (JSON as typeof JSON & { rawJSON(text: string): unknown }).rawJSON(item.toString());
    }
    if (typeof item === "number" && !Number.isFinite(item)) {
      throw new InvalidArgumentError("invalid_argument", "non-finite numbers cannot cross the bridge");
    }
    if (item === undefined || typeof item === "function" || typeof item === "symbol") {
      throw new InvalidArgumentError("invalid_argument", "undefined, functions and symbols cannot cross the bridge");
    }
    return item;
  });
  if (result === undefined) throw new InvalidArgumentError("invalid_argument", "expected a JSON value");
  return result;
}

export function camelCase(name: string): string {
  return name.split("_").map((part, i) => i && part ? part[0]!.toUpperCase() + part.slice(1) : part).join("");
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function transform(type: WireType, value: unknown, input: boolean, path: string): unknown {
  const fail = (message: string): never => {
    const ErrorType = input ? InvalidArgumentError : DaemonError;
    throw new ErrorType(input ? "invalid_argument" : "protocol", `${path}: ${message}`);
  };
  if (type.kind === "void") {
    if (value !== null && value !== undefined) return fail("expected null for a void result");
    return undefined;
  }
  if (type.kind === "ptr") {
    if (value === null) return null;
    return transform(type.elem!, value, input, path);
  }
  if ((type.kind === "slice" || type.kind === "map") && value === null) return null;
  switch (type.kind) {
    case "bytes":
      if (value === null) return null;
      if (input) {
        if (!(value instanceof Uint8Array)) return fail("expected Uint8Array or null");
        return Buffer.from(value).toString("base64");
      }
      if (typeof value !== "string" || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) return fail("expected base64 string");
      return new Uint8Array(Buffer.from(value, "base64"));
    case "timestamp":
      // Keep RFC 3339 text intact: Date would discard sub-millisecond precision.
      if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value)) return fail("expected RFC 3339 timestamp with timezone");
      return value;
    case "string":
      return typeof value === "string" ? value : fail("expected a string");
    case "bool":
      return typeof value === "boolean" ? value : fail("expected a boolean");
    case "duration":
    case "int64": {
      // Go emits int64 as decimal integer tokens. Safe parsed numbers can be
      // converted exactly; unsafe tokens were already preserved by parseWire.
      const result = !input && typeof value === "number" && Number.isSafeInteger(value) ? BigInt(value) : value;
      if (typeof result !== "bigint") return fail("expected bigint for int64 (use a literal such as 123n)");
      if (result < int64Min || result > int64Max) return fail("outside signed int64 range");
      return result;
    }
    case "int":
    case "int8":
    case "int16":
    case "int32": {
      if (typeof value !== "number" || !Number.isSafeInteger(value)) {
        return fail("expected a safe integer; expose Go int64 for the full 64-bit range");
      }
      if (type.kind !== "int") {
        const bits = Number(type.kind.slice(3));
        if (value < -(2 ** (bits - 1)) || value >= 2 ** (bits - 1)) return fail(`outside ${type.kind} range`);
      }
      return value;
    }
    case "float32":
    case "float64": {
      // Go may encode an integral float without a decimal point. Only a float
      // schema permits converting a preserved integer token back to Number.
      const result = !input && typeof value === "bigint" ? Number(value) : value;
      return typeof result === "number" && Number.isFinite(result) ? result : fail("expected a finite number");
    }
    case "slice":
      if (!Array.isArray(value)) return fail("expected an array or null");
      return value.map((item, i) => transform(type.elem!, item, input, `${path}[${i}]`));
    case "map":
      if (!record(value)) return fail("expected an object or null");
      // fromEntries creates own data properties for keys such as __proto__.
      return Object.fromEntries(Object.entries(value).map(([key, item]) =>
        [key, transform(type.elem!, item, input, `${path}[${JSON.stringify(key)}]`)]));
    case "struct": {
      if (!record(value)) return fail("expected an object");
      const fields = type.fields ?? [];
      const allowed = new Set(fields.map(field => input ? camelCase(field.name) : field.name));
      for (const key of Object.keys(value)) if (!allowed.has(key)) return fail(`unknown field ${key}`);
      const entries: [string, unknown][] = [];
      for (const field of fields) {
        const name = input ? camelCase(field.name) : field.name;
        const outputName = input ? field.name : camelCase(field.name);
        if (!Object.hasOwn(value, name) || (input && value[name] === undefined)) {
          if (field.type.kind === "ptr") continue;
          return fail(`missing field ${name}`);
        }
        entries.push([outputName, transform(field.type, value[name], input, `${path}.${name}`)]);
      }
      return Object.fromEntries(entries);
    }
    default:
      return fail(`unsupported wire type ${type.kind}`);
  }
}

/** Check native values and translate camelCase fields into Go wire names. */
export function encode(type: WireType, value: unknown): unknown {
  return transform(type, value, true, "params");
}

/** Reconstruct typed camelCase objects and exact int64 bigint values. */
export function decode<T>(type: WireType, value: unknown): T {
  return transform(type, value, false, "result") as T;
}

/** Parse generated metadata without rounding large numeric constraints. */
export function parseSchema(text: string): Schema {
  const schema = parseWire(text);
  if (!record(schema) || schema.protocol !== 1 || typeof schema.schema_hash !== "string" || !Array.isArray(schema.operations)) {
    throw new DaemonError("schema", "invalid generated Go schema");
  }
  return schema as unknown as Schema;
}
