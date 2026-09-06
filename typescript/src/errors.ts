export class BridgeError extends Error {
  constructor(readonly code: string, readonly message: string, readonly details: Readonly<Record<string, string>> = {}) {
    super(`${code}: ${message}`);
    this.name = new.target.name;
  }
}
export class InvalidArgumentError extends BridgeError {}
export class BusyError extends BridgeError {}
export class RequestTimeout extends BridgeError {}
export class DaemonError extends BridgeError {}
export class ClosedError extends BridgeError {}
export class AbortError extends BridgeError {}

/** Validate before removing a response's owner from the pending map. */
export function errorFromWire(value: unknown): BridgeError {
  if (typeof value !== "object" || value === null || Array.isArray(value) ||
      !("code" in value) || typeof value.code !== "string" ||
      !("message" in value) || typeof value.message !== "string") {
    throw new DaemonError("protocol", "error response needs string code and message");
  }
  const constructors: Record<string, typeof BridgeError> = {
    invalid_argument: InvalidArgumentError,
    busy: BusyError,
    deadline_exceeded: RequestTimeout,
    cancelled: AbortError,
  };
  const Constructor = Object.hasOwn(constructors, value.code) ? constructors[value.code]! : BridgeError;
  const details = "details" in value ? value.details : {};
  if (typeof details !== "object" || details === null || Array.isArray(details) || Object.values(details).some(v => typeof v !== "string")) throw new DaemonError("protocol", "invalid error details");
  return new Constructor(value.code, value.message, details as Record<string,string>);
}
