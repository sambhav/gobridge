import { test } from "node:test";
import assert from "node:assert/strict";
import { encode, decode, parseWire, stringifyWire, parseSchema } from "../dist/codec.js";
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
