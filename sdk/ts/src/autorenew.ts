/**
 * Background lease auto-renewal for the Janus TypeScript SDK.
 *
 * Nothing here starts on its own: a renew loop exists only after an explicit
 * {@link import("./dynamic.js").Lease.startAutoRenew} (or
 * {@link import("./client.js").JanusClient.withDynamic}, which calls it for
 * you). Every renew — success, retryable failure, and the single terminal
 * event — is reported through {@link AutoRenewOptions.onEvent}; the SDK itself
 * logs nothing, and no event ever carries the lease password.
 */

import type { Lease } from "./dynamic.js";
import {
  JanusError,
  JanusLeaseExpiredError,
  JanusMaxTtlReachedError,
} from "./errors.js";

/**
 * Fraction of the remaining TTL to wait before the next renew attempt.
 *
 * The policy is "renew at ~2/3 of the remaining TTL, with jitter": after every
 * successful renew the loop recomputes the time left until the lease expires
 * and waits this fraction of it before renewing again. That leaves roughly a
 * third of the TTL as headroom for retries if the server is briefly
 * unreachable.
 */
export const DEFAULT_RENEW_FRACTION = 2 / 3;

/**
 * Relative jitter applied to each computed wait (`0.1` == ±10%), so a fleet of
 * processes that started together does not renew in lockstep.
 */
export const DEFAULT_RENEW_JITTER = 0.1;

/**
 * Floor on the computed wait, in milliseconds, so a very short or already
 * elapsed TTL cannot turn the renewer into a hot loop.
 */
export const DEFAULT_MIN_RENEW_INTERVAL_MS = 1000;

/** Why a renew loop stopped. Reported on the terminal {@link RenewEvent}. */
export type StopReason =
  /** `stop()` was called. Not an error. */
  | "stopped"
  /** The `signal` passed to the renewer was aborted. */
  | "aborted"
  /**
   * The server will not extend the lease any further — it has reached
   * `max_expires_at`. Terminal by design, not a client failure.
   */
  | "max_ttl"
  /** The lease no longer exists or is no longer active (404 / 409). */
  | "lease_gone"
  /** The token was rejected (401). */
  | "unauthorized"
  /** The token may no longer renew this lease (403). */
  | "forbidden"
  /** The server rejected the renew with some other non-retryable 4xx. */
  | "rejected"
  /** The lease expired before any renew succeeded. */
  | "expired"
  /** Reported by `withDynamic` when the final revoke failed. */
  | "revoke_failed";

/**
 * One step of a renew loop. Value-free: it carries the lease ID and timings
 * only, never the lease password.
 */
export interface RenewEvent {
  /** The lease this event concerns. */
  leaseId: string;
  /** True when this event reports a successful renew. */
  renewed: boolean;
  /** The lease expiry known at the time of the event. */
  expiresAt: Date;
  /**
   * Set when a renew attempt failed. A non-terminal `error` is a retryable
   * failure (network error, 5xx, 408, 429): the loop will try again while TTL
   * headroom remains.
   */
  error?: unknown;
  /** True on the final event: the loop has stopped and will emit nothing further. */
  terminal: boolean;
  /** Set on the terminal event. */
  reason?: StopReason;
}

/** An opaque timer handle, whatever the injected timer factory returns. */
export type TimerHandle = unknown;

/** Options for {@link import("./dynamic.js").Lease.startAutoRenew}. */
export interface AutoRenewOptions {
  /**
   * Fraction of the remaining TTL to wait before the next renew attempt. Must
   * be in `(0, 1]`; anything else selects {@link DEFAULT_RENEW_FRACTION}.
   */
  fraction?: number;
  /**
   * Relative jitter applied to each wait (`0.1` == ±10%). Omit for
   * {@link DEFAULT_RENEW_JITTER}; pass `0` to disable jitter.
   */
  jitter?: number;
  /** Floor on the computed wait, in ms. Defaults to {@link DEFAULT_MIN_RENEW_INTERVAL_MS}. */
  minIntervalMs?: number;
  /**
   * Receives every renew event. Called synchronously from the renew loop, so it
   * must not block for long and must not `await renewer.stop()` (that would
   * deadlock on the loop it is running inside).
   *
   * This is the only place renew activity surfaces — the SDK logs nothing.
   */
  onEvent?: (event: RenewEvent) => void;
  /** Aborting this signal stops the renew loop (reason `"aborted"`). */
  signal?: AbortSignal;

  /** Clock in epoch milliseconds. Injectable so tests need no wall-clock waits. */
  now?: () => number;
  /** Timer factory. Defaults to `setTimeout` (unref'd where available). */
  setTimer?: (fn: () => void, ms: number) => TimerHandle;
  /** Timer canceller. Defaults to `clearTimeout`. */
  clearTimer?: (handle: TimerHandle) => void;
  /** Uniform random source in `[0, 1)` for jitter. Defaults to `Math.random`. */
  random?: () => number;
}

interface Resolved {
  fraction: number;
  jitter: number;
  minIntervalMs: number;
  onEvent?: (event: RenewEvent) => void;
  now: () => number;
  setTimer: (fn: () => void, ms: number) => TimerHandle;
  clearTimer: (handle: TimerHandle) => void;
  random: () => number;
}

function defaultSetTimer(fn: () => void, ms: number): TimerHandle {
  const handle = setTimeout(fn, ms) as unknown as { unref?: () => void };
  // In Node, an un-unref'd renew timer would keep the process alive after the
  // real work finished. The lease is still renewed for as long as anything else
  // holds the loop open.
  if (typeof handle?.unref === "function") {
    handle.unref();
  }
  return handle;
}

function resolveOptions(options: AutoRenewOptions | undefined): Resolved {
  const o = options ?? {};
  const fraction =
    typeof o.fraction === "number" && o.fraction > 0 && o.fraction <= 1
      ? o.fraction
      : DEFAULT_RENEW_FRACTION;
  const jitter =
    typeof o.jitter === "number" && o.jitter >= 0 ? o.jitter : DEFAULT_RENEW_JITTER;
  const minIntervalMs =
    typeof o.minIntervalMs === "number" && o.minIntervalMs > 0
      ? o.minIntervalMs
      : DEFAULT_MIN_RENEW_INTERVAL_MS;
  return {
    fraction,
    jitter,
    minIntervalMs,
    onEvent: o.onEvent,
    now: o.now ?? Date.now,
    setTimer: o.setTimer ?? defaultSetTimer,
    clearTimer: o.clearTimer ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>)),
    random: o.random ?? Math.random,
  };
}

/**
 * How long to wait before the next renew attempt, given the time left before
 * the lease expires. `retry` halves the fraction so a failed attempt is retried
 * sooner than the normal cadence while still converging on the expiry instead
 * of hot-looping.
 *
 * @internal
 */
export function computeWaitMs(o: Resolved, remainingMs: number, retry: boolean): number {
  const frac = retry ? o.fraction / 2 : o.fraction;
  let wait = remainingMs * frac;
  if (o.jitter > 0) {
    wait *= 1 + o.jitter * (2 * o.random() - 1);
  }
  if (wait < o.minIntervalMs) {
    wait = o.minIntervalMs;
  }
  if (wait > remainingMs) {
    wait = remainingMs;
  }
  return wait < 0 ? 0 : wait;
}

/**
 * Decide whether a failed renew ends the loop. 4xx responses are terminal (the
 * server will keep saying no), except 408 and 429 which are transient.
 * Everything else — network errors, 5xx, a sealed server — is retryable while
 * TTL headroom remains.
 *
 * @internal
 */
export function classifyRenewError(err: unknown): StopReason | null {
  if (!(err instanceof JanusError)) {
    return null;
  }
  switch (err.status) {
    case 401:
      return "unauthorized";
    case 403:
      return "forbidden";
    case 404:
    case 409:
      return "lease_gone";
    case 408:
    case 429:
      return null;
    default:
      break;
  }
  if (err.status >= 400 && err.status < 500) {
    return "rejected";
  }
  return null;
}

/**
 * A running background renew loop for a single lease. Create one with
 * {@link import("./dynamic.js").Lease.startAutoRenew}; the caller owns it and
 * must {@link LeaseRenewer.stop} it.
 */
export class LeaseRenewer {
  /** Resolves once the loop has exited, however it ended. Never rejects. */
  readonly done: Promise<void>;

  private readonly lease: Lease;
  private readonly opts: Resolved;
  private readonly controller = new AbortController();
  private stopRequested = false;
  private aborted = false;
  private wake: (() => void) | null = null;
  private terminalError: unknown = undefined;
  private terminalReason: StopReason | undefined = undefined;
  private readonly onAbort: () => void;
  private readonly externalSignal: AbortSignal | undefined;

  /** @internal — use `Lease.startAutoRenew`. */
  constructor(lease: Lease, options?: AutoRenewOptions) {
    this.lease = lease;
    this.opts = resolveOptions(options);
    this.externalSignal = options?.signal;
    this.onAbort = () => {
      this.aborted = true;
      this.requestStop();
    };
    if (this.externalSignal) {
      if (this.externalSignal.aborted) {
        this.aborted = true;
        this.stopRequested = true;
      } else {
        this.externalSignal.addEventListener("abort", this.onAbort, { once: true });
      }
    }
    this.done = this.loop();
  }

  /**
   * The terminal error that ended the loop, or `undefined` if it stopped
   * cleanly. Read it after `await renewer.done`.
   */
  get error(): unknown {
    return this.terminalError;
  }

  /** Why the loop ended. Read it after `await renewer.done`. */
  get reason(): StopReason | undefined {
    return this.terminalReason;
  }

  /**
   * Stop the loop, abort any renew request in flight, and resolve once the loop
   * has actually exited. Idempotent: calling it again (or after the loop
   * already ended on its own) resolves immediately and releases nothing twice.
   *
   * Never call it from an `onEvent` handler — that handler runs inside the loop
   * this waits for.
   */
  async stop(): Promise<void> {
    this.requestStop();
    await this.done;
  }

  private requestStop(): void {
    if (this.stopRequested) {
      return;
    }
    this.stopRequested = true;
    this.controller.abort();
    const wake = this.wake;
    this.wake = null;
    wake?.();
  }

  private emit(event: RenewEvent): void {
    this.opts.onEvent?.(event);
  }

  private finish(error: unknown, reason: StopReason, expiresAt: Date): void {
    this.terminalError = error;
    this.terminalReason = reason;
    if (this.externalSignal) {
      this.externalSignal.removeEventListener("abort", this.onAbort);
    }
    this.emit({
      leaseId: this.lease.id,
      renewed: false,
      expiresAt,
      error: error ?? undefined,
      terminal: true,
      reason,
    });
  }

  /** Sleep, resolving early with `"stopped"` if the loop is asked to stop. */
  private sleep(ms: number): Promise<"timer" | "stopped"> {
    return new Promise((resolve) => {
      if (this.stopRequested) {
        resolve("stopped");
        return;
      }
      let settled = false;
      let handle: TimerHandle = undefined;
      const wake = () => {
        if (settled) return;
        settled = true;
        this.wake = null;
        this.opts.clearTimer(handle);
        resolve("stopped");
      };
      handle = this.opts.setTimer(() => {
        if (settled) return;
        settled = true;
        this.wake = null;
        resolve("timer");
      }, ms);
      if (settled) {
        // The timer fired synchronously; there is nothing left to cancel.
        return;
      }
      // A stop that landed while the timer was being armed must still win.
      if (this.stopRequested) {
        wake();
        return;
      }
      this.wake = wake;
    });
  }

  private stoppedReason(): StopReason {
    return this.aborted ? "aborted" : "stopped";
  }

  private async loop(): Promise<void> {
    let retry = false;
    for (;;) {
      const expiry = this.lease.expiresAt;
      const remaining = expiry.getTime() - this.opts.now();
      if (remaining <= 0) {
        this.finish(new JanusLeaseExpiredError(this.lease.id), "expired", expiry);
        return;
      }

      const woke = await this.sleep(computeWaitMs(this.opts, remaining, retry));
      if (woke === "stopped") {
        this.finish(undefined, this.stoppedReason(), expiry);
        return;
      }
      retry = false;

      let view;
      try {
        view = await this.lease.renewView({ signal: this.controller.signal });
      } catch (err) {
        // A stop request beats every other interpretation: aborting the
        // in-flight request is how stop() unblocks promptly.
        if (this.stopRequested) {
          this.finish(undefined, this.stoppedReason(), expiry);
          return;
        }
        const reason = classifyRenewError(err);
        if (reason !== null) {
          this.finish(err, reason, expiry);
          return;
        }
        this.emit({
          leaseId: this.lease.id,
          renewed: false,
          expiresAt: expiry,
          error: err,
          terminal: false,
        });
        retry = true;
        continue;
      }

      const next = this.lease.expiresAt;
      this.emit({ leaseId: this.lease.id, renewed: true, expiresAt: next, terminal: false });

      // Renewal is capped server-side. Two independent signals that the cap has
      // been reached: the server reports expires_at at/after max_expires_at, or
      // the renew simply did not move the expiry forward.
      const max = view.maxExpiresAt;
      const atMax = max !== undefined && next.getTime() >= max.getTime();
      if (atMax || next.getTime() <= expiry.getTime()) {
        this.finish(new JanusMaxTtlReachedError(this.lease.id), "max_ttl", next);
        return;
      }
    }
  }
}
