/** The Go wire schema. Names remain the original JSON names in this metadata. */
export interface Constraints {
  readonly minimum?: number | bigint;
  readonly maximum?: number | bigint;
  readonly min_length?: number;
  readonly max_length?: number;
}
export interface Field {
 readonly required?:boolean;
 readonly optional?:boolean;
 readonly non_null?:boolean;
  readonly public_name?: string;
  readonly name: string;
  readonly type: WireType;
  readonly description?: string;
  readonly constraints?: Constraints;
}
export interface WireType {
  readonly kind: string;
  readonly enum?: readonly {name:string; value:string | number | bigint}[];
  readonly length?: number;
  readonly name?: string;
  readonly elem?: WireType;
  readonly fields?: readonly Field[];
}
export interface Operation {
  readonly stream?: boolean;
  readonly name: string;
  readonly description: string;
  readonly input: WireType;
  readonly output: WireType;
}
export interface Schema {
  readonly protocol: number;
  readonly schema_hash: string;
  readonly operations: readonly Operation[];
  readonly constructor?: WireType;
}
