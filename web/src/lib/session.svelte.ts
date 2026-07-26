/* Gate state backed by the real /v1 API: init → unseal → login → ready. */

import { api, setUnauthenticatedHandler, type SealStatus, type Me, type InitResult } from './api'

export type Phase = 'loading' | 'unreachable' | 'uninitialized' | 'sealed' | 'login' | 'ready'

let phase = $state<Phase>('loading')
let seal = $state<SealStatus | null>(null)
let me = $state<Me | null>(null)

async function resolveAuthed(): Promise<void> {
  try {
    me = await api.me()
    phase = 'ready'
  } catch {
    me = null
    phase = 'login'
  }
}

export const session = {
  get phase() { return phase },
  get seal() { return seal },
  get me() { return me },
  get sharesSubmitted() { return seal?.progress?.submitted ?? 0 },
  get threshold() { return seal?.threshold ?? seal?.progress?.required ?? 3 },
  get totalShares() { return seal?.shares ?? 5 },
  get sealType() { return seal?.type ?? 'shamir' },

  async bootstrap() {
    try {
      seal = await api.sealStatus()
    } catch {
      phase = 'unreachable'
      return
    }
    if (!seal.initialized) phase = 'uninitialized'
    else if (seal.sealed) phase = 'sealed'
    else await resolveAuthed()
  },

  async init(shares: number, threshold: number, adminEmail: string): Promise<InitResult> {
    const res = await api.init(shares, threshold, adminEmail)
    seal = await api.sealStatus()
    return res
  },

  /** After the init ceremony's credentials have been acknowledged. */
  async proceedFromInit() {
    seal = await api.sealStatus()
    phase = seal.sealed ? 'sealed' : 'login'
    if (!seal.sealed) await resolveAuthed()
  },

  /** Returns true when the threshold was reached and the vault unsealed. */
  async submitShare(share: string): Promise<boolean> {
    seal = await api.unsealShare(share)
    if (!seal.sealed) {
      setTimeout(() => void resolveAuthed(), 1400)
      return true
    }
    return false
  },

  async unsealKms(): Promise<boolean> {
    seal = await api.unsealKms()
    if (!seal.sealed) {
      setTimeout(() => void resolveAuthed(), 800)
      return true
    }
    return false
  },

  async login(email: string, password: string, totpCode?: string) {
    await api.login(email, password, totpCode)
    await resolveAuthed()
  },

  async logout() {
    try { await api.logout() } catch { /* session may already be gone */ }
    me = null
    phase = 'login'
  },

  async sealServer() {
    await api.seal()
    seal = await api.sealStatus()
    me = null
    phase = 'sealed'
  },

  /** Called by the API layer on 401/sealed responses. */
  async refresh() {
    await this.bootstrap()
  },

  /** Drops to the login gate after the API layer sees a 401 on a normal
      request. Kept separate from logout(): there is no live session left to
      revoke, so calling the logout endpoint would just 401 again. */
  expire() {
    if (phase !== 'ready') return // already gated; nothing to do
    me = null
    phase = 'login'
  },
}

/* Wire the API layer's 401 hook to the gate. Without this an expired session
   left the shell rendered and every action failed with a feature-level error
   that never mentioned authentication. */
setUnauthenticatedHandler(() => session.expire())
