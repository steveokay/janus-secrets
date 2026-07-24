/**
 * `janus-client` — a typed TypeScript client for the Janus secrets manager's
 * `/v1` REST API.
 *
 * Programmatic, in-process secret reads with a memory-only TTL cache and
 * optional dynamic-credential lease management. Mirrors the Janus Go SDK.
 *
 * The cache is memory-only — secret values are never written to disk, and no
 * method logs secret values.
 *
 * @packageDocumentation
 */

export {
  JanusClient,
  DEFAULT_CACHE_TTL_MS,
  type JanusClientOptions,
  type FetchLike,
  type RequestOptions,
} from "./client.js";

export { Lease, type LeaseData } from "./dynamic.js";

export {
  JanusError,
  JanusUnauthorizedError,
  JanusForbiddenError,
  JanusNotFoundError,
  JanusSealedError,
  isUnauthorized,
  isForbidden,
  isNotFound,
  isSealed,
} from "./errors.js";
