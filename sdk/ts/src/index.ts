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
  type WithDynamicOptions,
} from "./client.js";

export { Lease, type LeaseData, type ParsedLeaseView } from "./dynamic.js";

export {
  LeaseRenewer,
  DEFAULT_RENEW_FRACTION,
  DEFAULT_RENEW_JITTER,
  DEFAULT_MIN_RENEW_INTERVAL_MS,
  type AutoRenewOptions,
  type RenewEvent,
  type StopReason,
  type TimerHandle,
} from "./autorenew.js";

export {
  JanusError,
  JanusUnauthorizedError,
  JanusForbiddenError,
  JanusNotFoundError,
  JanusSealedError,
  JanusMaxTtlReachedError,
  JanusLeaseExpiredError,
  JanusRevokeError,
  isUnauthorized,
  isForbidden,
  isNotFound,
  isSealed,
} from "./errors.js";
