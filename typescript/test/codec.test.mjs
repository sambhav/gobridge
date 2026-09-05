import { test } from "node:test";
import assert from "node:assert/strict";
import { encode, decode, compileCodec, parseWire, stringifyWire, parseSchema } from "../dist/codec.js";
import { InvalidArgumentError, DaemonError, errorFromWire, BridgeError, AbortError } from "../dist/errors.js";

const int64 = {kind: "int64"};
test("int64 tokens and results round trip without Number rounding", () => {
  for (const value of [0n, 1n, -(2n**63n), 2n**63n-1n, 9007199254740993n]) {
    assert.equal(decode(int64, parseWire(stringifyWire(encode(int64, value)))), value);
  }
  assert.throws(() => encode(int64, 42), InvalidArgumentError);
  assert.throws(() => encode(int64, 2n**63n), InvalidArgumentError);
  assert.throws(() => decode(int64, 9007199254740992), DaemonError);
});

test("safe integers and floating point semantics are distinct", () => {
  assert.equal(decode({kind:"int"}, 42), 42);
  assert.throws(() => decode({kind:"int"}, parseWire("9007199254740993")), DaemonError);
  assert.throws(() => encode({kind:"int8"}, 128), InvalidArgumentError);
  assert.throws(() => encode({kind:"int"}, 1.5), InvalidArgumentError);
  assert.equal(decode({kind:"float64"}, parseWire("100000000000000000000")), 1e20);
  assert.throws(() => encode({kind:"float64"}, Infinity), InvalidArgumentError);
});

test("JSON does not silently coerce non-values or non-finite numbers", () => {
  for (const value of [NaN, Infinity, undefined, [undefined], {bad:undefined}, {bad:()=>{}}]) {
    assert.throws(() => stringifyWire(value), InvalidArgumentError);
  }
  assert.throws(() => parseWire("1e999"), DaemonError);
});

test("nested fields translate names and preserve nullable containers", () => {
  const model = {kind:"struct",fields:[
    {name:"process_id",type:{kind:"int"}},
    {name:"child_list",type:{kind:"slice",elem:{kind:"struct",fields:[{name:"big_count",type:int64}]}}},
    {name:"optional_name",type:{kind:"ptr",elem:{kind:"string"}}},
  ]};
  const value = {processId:123,childList:[{bigCount:9007199254740993n}]};
  assert.deepEqual(encode(model,value), {process_id:123,child_list:[{big_count:9007199254740993n}]});
  assert.deepEqual(decode(model,parseWire(stringifyWire(encode(model,value)))),value);
  assert.deepEqual(decode(model,{process_id:123,child_list:null,optional_name:null}), {processId:123,childList:null,optionalName:null});
  assert.throws(() => encode(model,{...value,extra:true}), /unknown field/);
  assert.throws(() => encode(model,{childList:[]}), /missing field processId/);
});

test("map keys are data, including __proto__ and constructor", () => {
  const map = {kind:"map",elem:{kind:"string"}};
  const value = JSON.parse('{"__proto__":"safe","constructor":"also safe"}');
  const result = decode(map,encode(map,value));
  assert.equal(Object.getPrototypeOf(result),Object.prototype);
  assert.equal(Object.hasOwn(result,"__proto__"),true);
  assert.equal(result.__proto__,"safe");
  assert.equal(result.constructor,"also safe");
});

test("schema constraints preserve full-width integers", () => {
  const schema = parseSchema('{"protocol":1,"schema_hash":"x","operations":[],"constructor":{"kind":"struct","fields":[{"name":"limit","type":{"kind":"int64"},"constraints":{"minimum":9007199254740993}}]}}');
  assert.equal(schema.constructor.fields[0].constraints.minimum,9007199254740993n);
  assert.throws(() => parseSchema("null"),DaemonError);
});

test("malformed errors cannot strand pending requests", () => {
  for (const value of [null,[],{}, {code:[],message:"x"},{code:"x",message:1}]) assert.throws(()=>errorFromWire(value),DaemonError);
  assert.ok(errorFromWire({code:"cancelled",message:"x"}) instanceof AbortError);
  assert.ok(errorFromWire({code:"constructor",message:"x"}) instanceof BridgeError);
});

test("compiled codecs preserve generic values, rejection and field paths", () => {
  const scalarCases = [
    ['string', ['ok', null, 1]], ['bool', [true, null, 'true']],
    ['int8', [-128, 127, 128, 1.5, null]], ['int', [42, 2**54, null]],
    ['int64', [0n, 9007199254740993n, 2n**63n, 42, null]],
    ['float64', [1.5, 2n**64n, Infinity, null]],
    ['bytes', [new Uint8Array([0,255]), 'AP8=', 'bad!', null]],
    ['timestamp', ['2026-01-01T00:00:00.123456789Z', 'bad', null]],
    ['void', [undefined, null, 1]],
  ];
  const outcome = call => {
    try { return {value:call()}; }
    catch (error) { return {error:error.constructor.name,code:error.code,message:error.message}; }
  };
  for (const [kind, values] of scalarCases) {
    const scalar = {kind};
    for (const type of [scalar, {kind:'ptr',elem:scalar}, {kind:'slice',elem:scalar},
      {kind:'map',elem:scalar}, {kind:'struct',fields:[{name:'some_value',type:scalar}]}]) {
      const codec = compileCodec(type);
      for (const value of values) {
        const variants = [value, [value], {someValue:value}, {some_value:value}, {},
          {unexpected:value}, Object.fromEntries([['__proto__',value],['constructor',value]])];
        for (const variant of variants) {
          assert.deepEqual(outcome(()=>codec.encode(variant)),outcome(()=>encode(type,variant)));
          assert.deepEqual(outcome(()=>codec.decode(variant)),outcome(()=>decode(type,variant)));
        }
      }
    }
  }
});

test("compiled schema snapshot is independent of later metadata edits", () => {
  const type = {kind:'struct',fields:[{name:'old_name',type:{kind:'string'}}]};
  const codec = compileCodec(type);
  type.fields[0].name = 'new_name';
  type.fields[0].type.kind = 'int';
  assert.deepEqual(codec.encode({oldName:'value'}),{old_name:'value'});
  assert.throws(()=>codec.encode({newName:1}), /unknown field/);
});

test("compiled struct fields cannot change the output prototype", () => {
  const type = {kind:'struct',fields:[{name:'__proto__',type:{kind:'string'}}]};
  // camelCase preserves this codec's existing spelling conversion in both modes.
  const value = Object.fromEntries([['__proto__','data']]);
  assert.deepEqual(compileCodec(type).decode(value), decode(type,value));
  const encoded = compileCodec(type).encode({Proto:'data'});
  assert.equal(Object.getPrototypeOf(encoded),Object.prototype);
  assert.equal(Object.hasOwn(encoded,'__proto__'),true);
  assert.equal(encoded.__proto__,'data');
});
