/**
 * Group types for the Janus TypeScript SDK.
 *
 * A **group** is a subject a role binding can target instead of a user, so a
 * whole team is granted access once. Everything here is part of the instance
 * group **catalog** and needs `group:manage` (admin or owner) — a config- or
 * environment-scoped `janus_svc_...` read token, the usual credential for this
 * SDK, is rejected with {@link JanusForbiddenError} by all of it except
 * {@link JanusClient.myGroups}.
 *
 * Group *bindings* (granting a group a role at a scope) are deliberately not in
 * this SDK — see `docs/guides/typescript-sdk.md`. They are a different
 * authority, and Terraform's `janus_group_binding` is the supported way to
 * manage them declaratively.
 */

/** A group is IdP-fed (`oidc`) or admin-curated (`local`), and never both. */
export type GroupKind = "oidc" | "local";

/** `oidc` — membership comes from the identity provider's group claim. */
export const GROUP_KIND_OIDC = "oidc";
/** `local` — membership is an explicit list managed in Janus. */
export const GROUP_KIND_LOCAL = "local";

/** A Janus group. */
export interface Group {
  /** Group UUID. */
  id: string;
  /**
   * Group name, unique across BOTH kinds — so an IdP group and a local group
   * can never quietly become the same group.
   */
  name: string;
  /** `oidc` or `local`. */
  kind: GroupKind;
  /**
   * The exact value the identity provider emits for this group. Set only for
   * `oidc` groups; `null` otherwise.
   */
  claimValue: string | null;
  /** Free-text description (display material, never secret material). */
  description: string;
  /**
   * The narrow delegated project-creation capability. Deliberately a capability
   * rather than a role: any role carrying `project:create` at instance scope
   * would also carry `project:read` there, revealing every project.
   */
  canCreateProjects: boolean;
  /**
   * How many users Janus has **recorded** in this group.
   *
   * Deliberately not called `memberCount`. For an `oidc` group, membership is a
   * snapshot refreshed at each sign-in, so this counts only users who have
   * actually signed in — never the identity provider's membership list. Do not
   * present it as the size of the team.
   */
  membersSeen: number;
  /** How many scopes the group is bound at. */
  bindingCount: number;
  /** When the group was created (ISO-8601 as returned by the server). */
  createdAt: string;
}

/**
 * One recorded membership row.
 *
 * For an `oidc` group, a list of these covers only users who have signed in: a
 * member the IdP knows about who has never logged into Janus does not appear,
 * because Janus has never seen a token for them.
 */
export interface GroupMember {
  /** Janus user UUID. */
  userId: string;
  /**
   * When the membership row was created — when an admin added the user for a
   * `local` group, or when a login sync first recorded them for an `oidc` one.
   */
  addedAt: string;
}

/** Payload for {@link JanusClient.createGroup}. */
export interface GroupInput {
  /** Required; unique across both kinds. */
  name: string;
  /** Required. */
  kind: GroupKind;
  /** Required for `oidc`, forbidden for `local`. */
  claimValue?: string;
  /** Optional free text. */
  description?: string;
  /** Delegate project creation to this group. */
  canCreateProjects?: boolean;
}

/** Wire shape of a group as returned by `/v1/groups`. @internal */
export interface GroupWire {
  id?: string;
  name?: string;
  kind?: string;
  claim_value?: string | null;
  description?: string;
  can_create_projects?: boolean;
  member_count?: number;
  binding_count?: number;
  created_at?: string;
}

/** Wire shape of one member row. @internal */
export interface GroupMemberWire {
  user_id?: string;
  created_at?: string;
}

/** Map a wire group onto the camelCase interface. @internal */
export function parseGroup(w: GroupWire): Group {
  return {
    id: w.id ?? "",
    name: w.name ?? "",
    kind: (w.kind === GROUP_KIND_OIDC ? GROUP_KIND_OIDC : GROUP_KIND_LOCAL) as GroupKind,
    claimValue: w.claim_value ?? null,
    description: w.description ?? "",
    canCreateProjects: w.can_create_projects ?? false,
    membersSeen: w.member_count ?? 0,
    bindingCount: w.binding_count ?? 0,
    createdAt: w.created_at ?? "",
  };
}

/** Map a wire member row. @internal */
export function parseGroupMember(w: GroupMemberWire): GroupMember {
  return { userId: w.user_id ?? "", addedAt: w.created_at ?? "" };
}

/** Build the create body, dropping the fields the server must not see. @internal */
export function groupCreateBody(input: GroupInput): Record<string, unknown> {
  const body: Record<string, unknown> = { name: input.name, kind: input.kind };
  if (input.claimValue) body.claim_value = input.claimValue;
  if (input.description) body.description = input.description;
  if (input.canCreateProjects) body.can_create_projects = true;
  return body;
}

/**
 * Enforce the two-kinds rule locally, so an impossible group never costs a
 * round trip. Throws a plain `Error` (a client-side misuse, not an API error).
 *
 * @internal
 */
export function validateGroupInput(input: GroupInput): void {
  if (!input?.name) {
    throw new Error("janus: group name is required");
  }
  if (input.kind === GROUP_KIND_LOCAL) {
    if (input.claimValue) {
      throw new Error(
        "janus: a local group cannot have a claim value; its membership is the explicit member list",
      );
    }
    return;
  }
  if (input.kind === GROUP_KIND_OIDC) {
    if (!input.claimValue) {
      throw new Error(
        "janus: an oidc group requires a claim value; without one it matches nothing a token can assert",
      );
    }
    return;
  }
  throw new Error(
    `janus: group kind must be "${GROUP_KIND_OIDC}" or "${GROUP_KIND_LOCAL}", got ${JSON.stringify(input.kind)}`,
  );
}
