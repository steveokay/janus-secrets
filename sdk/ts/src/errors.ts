/**
 * Typed errors for the Janus TypeScript SDK.
 *
 * Non-2xx responses are parsed from the server's `{"error":{code,message}}`
 * envelope into a {@link JanusError} carrying the HTTP `status`, the
 * machine-readable `code`, and the human `message`. Specific subclasses map the
 * common statuses (401/403/404/503) so callers can branch with `instanceof` or
 * the exported type guards.
 *
 * A `JanusError` never carries a secret value: the server's error envelope is
 * value-free by design, and these types only ever hold the parsed code and
 * message.
 */

/** Base error for every failed Janus API call. */
export class JanusError extends Error {
  /** HTTP status code of the response (e.g. 403, 404, 503). */
  readonly status: number;
  /** Machine-readable error code from the envelope (e.g. "forbidden", "sealed"). */
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message || `janus: api error (status ${status})`);
    this.name = "JanusError";
    this.status = status;
    this.code = code;
    // Restore prototype chain for correct instanceof across the class hierarchy.
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

/** The token was missing, invalid, or expired (HTTP 401). */
export class JanusUnauthorizedError extends JanusError {
  constructor(status: number, code: string, message: string) {
    super(status, code, message);
    this.name = "JanusUnauthorizedError";
  }
}

/** The token is authenticated but not authorized for the operation (HTTP 403). */
export class JanusForbiddenError extends JanusError {
  constructor(status: number, code: string, message: string) {
    super(status, code, message);
    this.name = "JanusForbiddenError";
  }
}

/** The requested config, key, or resource does not exist or is not visible (HTTP 404). */
export class JanusNotFoundError extends JanusError {
  constructor(status: number, code: string, message: string) {
    super(status, code, message);
    this.name = "JanusNotFoundError";
  }
}

/**
 * The server is sealed and cannot serve secret operations until unsealed
 * (HTTP 503, or error code "sealed"). Retry after the server is unsealed.
 */
export class JanusSealedError extends JanusError {
  constructor(status: number, code: string, message: string) {
    super(status, code, message);
    this.name = "JanusSealedError";
  }
}

/** True if `err` is a {@link JanusUnauthorizedError} (401). */
export function isUnauthorized(err: unknown): err is JanusUnauthorizedError {
  return err instanceof JanusUnauthorizedError;
}

/** True if `err` is a {@link JanusForbiddenError} (403). */
export function isForbidden(err: unknown): err is JanusForbiddenError {
  return err instanceof JanusForbiddenError;
}

/** True if `err` is a {@link JanusNotFoundError} (404). */
export function isNotFound(err: unknown): err is JanusNotFoundError {
  return err instanceof JanusNotFoundError;
}

/** True if `err` is a {@link JanusSealedError} (503 / "sealed"). */
export function isSealed(err: unknown): err is JanusSealedError {
  return err instanceof JanusSealedError;
}

/** Shape of the server error envelope: `{"error":{"code","message"}}`. */
interface ErrorEnvelope {
  error?: { code?: string; message?: string };
}

/**
 * Construct the most specific {@link JanusError} for a failed response, parsing
 * the `{"error":{code,message}}` envelope when present. The `sealed` code and a
 * 503 status both map to {@link JanusSealedError}, so the mapping stays stable
 * even if the server refines its code strings.
 *
 * @internal
 */
export async function parseApiError(res: Response): Promise<JanusError> {
  let code = "";
  let message = "";
  try {
    const text = await res.text();
    if (text) {
      const env = JSON.parse(text) as ErrorEnvelope;
      code = env.error?.code ?? "";
      message = env.error?.message ?? "";
    }
  } catch {
    // Non-JSON or unreadable body: fall through with empty code/message.
  }

  const status = res.status;
  if (status === 401) return new JanusUnauthorizedError(status, code, message);
  if (status === 403) return new JanusForbiddenError(status, code, message);
  if (status === 404) return new JanusNotFoundError(status, code, message);
  if (status === 503 || code === "sealed") {
    return new JanusSealedError(status, code, message);
  }
  return new JanusError(status, code, message);
}
