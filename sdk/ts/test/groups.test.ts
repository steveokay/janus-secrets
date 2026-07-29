import assert from "node:assert/strict";
import { test } from "node:test";

import {
  GROUP_KIND_LOCAL,
  GROUP_KIND_OIDC,
  JanusClient,
  JanusError,
  JanusForbiddenError,
  isForbidden,
} from "../src/index.js";
import { TEST_TOKEN, errorResponse, fakeFetch, jsonResponse } from "./helpers.js";

const BASE = "https://janus.example.test";

// obviously-fake fixtures (not real identifiers)
const GROUP_ID = "grp-00000000-0000-0000-0000-000000000001";
const USER_ID = "usr-00000000-0000-0000-0000-000000000002";
const CLAIM_VALUE = "8f14e45f-ceea-467a-9d0e-7f4b2a1c9c33";

function client(fetch: ReturnType<typeof fakeFetch>["fetch"]): JanusClient {
  return new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
}

test("createGroup posts the wire shape and parses the response", async () => {
  const { fetch, requests } = fakeFetch({
    "POST /v1/groups": () =>
      jsonResponse(201, {
        id: GROUP_ID,
        name: "Team Payments",
        kind: "oidc",
        claim_value: CLAIM_VALUE,
        member_count: 2,
        binding_count: 1,
        created_at: "2026-07-29T10:00:00Z",
      }),
  });
  const c = client(fetch);

  const group = await c.createGroup({
    name: "Team Payments",
    kind: GROUP_KIND_OIDC,
    claimValue: CLAIM_VALUE,
  });

  assert.deepEqual(requests[0].json, {
    name: "Team Payments",
    kind: "oidc",
    claim_value: CLAIM_VALUE,
  });
  assert.equal(requests[0].authorization, `Bearer ${TEST_TOKEN}`);
  assert.equal(group.kind, "oidc");
  assert.equal(group.claimValue, CLAIM_VALUE);
  // Deliberately `membersSeen`, not `memberCount`: for an oidc group this only
  // counts users who have signed in.
  assert.equal(group.membersSeen, 2);
  assert.equal(group.bindingCount, 1);
});

test("a local group never ships a claim value", async () => {
  const { fetch, requests } = fakeFetch({
    "POST /v1/groups": () => jsonResponse(201, { id: GROUP_ID, kind: "local" }),
  });
  const group = await client(fetch).createGroup({ name: "Platform", kind: GROUP_KIND_LOCAL });

  assert.deepEqual(requests[0].json, { name: "Platform", kind: "local" });
  assert.equal(group.claimValue, null);
});

test("the two-kinds rule is enforced locally, before any request", async () => {
  const { fetch, requests } = fakeFetch({
    "POST /v1/groups": () => jsonResponse(201, { id: GROUP_ID }),
  });
  const c = client(fetch);

  await assert.rejects(
    () => c.createGroup({ name: "t", kind: GROUP_KIND_OIDC }),
    /requires a claim value/,
  );
  await assert.rejects(
    () => c.createGroup({ name: "t", kind: GROUP_KIND_LOCAL, claimValue: "x" }),
    /cannot have a claim value/,
  );
  await assert.rejects(
    // A kind from untyped input (JSON config, CLI flag) still fails cleanly.
    () => c.createGroup({ name: "t", kind: "ldap" as never }),
    /group kind must be/,
  );
  await assert.rejects(() => c.createGroup({ name: "", kind: GROUP_KIND_LOCAL }), /name is required/);

  assert.equal(requests.length, 0, "no request should be issued for an invalid group");
});

test("listGroups and listGroupMembers walk every cursor page", async () => {
  const { fetch } = fakeFetch({
    "GET /v1/groups?limit=100": () =>
      jsonResponse(200, {
        groups: [{ id: "g1", kind: "local" }],
        next_cursor: "page2",
      }),
    "GET /v1/groups?limit=100&cursor=page2": () =>
      jsonResponse(200, { groups: [{ id: "g2", kind: "oidc" }], next_cursor: null }),
    [`GET /v1/groups/${GROUP_ID}/members?limit=100`]: () =>
      jsonResponse(200, {
        members: [{ user_id: "u1", created_at: "2026-07-01T00:00:00Z" }],
        next_cursor: "page2",
      }),
    [`GET /v1/groups/${GROUP_ID}/members?limit=100&cursor=page2`]: () =>
      jsonResponse(200, { members: [{ user_id: "u2" }], next_cursor: null }),
  });
  const c = client(fetch);

  const groups = await c.listGroups();
  assert.deepEqual(
    groups.map((g) => g.id),
    ["g1", "g2"],
  );

  const members = await c.listGroupMembers(GROUP_ID);
  assert.deepEqual(
    members.map((m) => m.userId),
    ["u1", "u2"],
  );
  assert.equal(members[0].addedAt, "2026-07-01T00:00:00Z");
});

test("getGroup unwraps the {group, bindings} envelope", async () => {
  const { fetch } = fakeFetch({
    [`GET /v1/groups/${GROUP_ID}`]: () =>
      jsonResponse(200, {
        group: { id: GROUP_ID, name: "Team Payments", kind: "oidc", claim_value: CLAIM_VALUE },
        bindings: [{ group_id: GROUP_ID, role: "developer" }],
      }),
  });
  const group = await client(fetch).getGroup(GROUP_ID);
  assert.equal(group.name, "Team Payments");
  assert.equal(group.claimValue, CLAIM_VALUE);
});

test("membership and capability writes hit the right routes", async () => {
  const memberPath = `/v1/groups/${GROUP_ID}/members/${USER_ID}`;
  const { fetch, requests } = fakeFetch({
    [`PUT ${memberPath}`]: () => new Response(null, { status: 204 }),
    [`DELETE ${memberPath}`]: () => new Response(null, { status: 204 }),
    [`PUT /v1/groups/${GROUP_ID}/capabilities`]: () =>
      jsonResponse(200, { can_create_projects: true }),
    [`DELETE /v1/groups/${GROUP_ID}`]: () => new Response(null, { status: 204 }),
  });
  const c = client(fetch);

  await c.addGroupMember(GROUP_ID, USER_ID);
  await c.removeGroupMember(GROUP_ID, USER_ID);
  await c.setGroupProjectCreation(GROUP_ID, true);
  await c.deleteGroup(GROUP_ID);

  assert.deepEqual(
    requests.map((r) => `${r.method} ${r.url.replace(BASE, "")}`),
    [
      `PUT ${memberPath}`,
      `DELETE ${memberPath}`,
      `PUT /v1/groups/${GROUP_ID}/capabilities`,
      `DELETE /v1/groups/${GROUP_ID}`,
    ],
  );
  assert.deepEqual(requests[2].json, { can_create_projects: true });
  // A membership PUT carries no body at all.
  assert.equal(requests[0].body, null);
});

test("adding a member to an oidc group surfaces the server's 409", async () => {
  const { fetch } = fakeFetch({
    [`PUT /v1/groups/${GROUP_ID}/members/${USER_ID}`]: () =>
      errorResponse(
        409,
        "validation",
        "membership of an oidc group comes from the identity provider",
      ),
  });
  await assert.rejects(client(fetch).addGroupMember(GROUP_ID, USER_ID), (err: unknown) => {
    assert.ok(err instanceof JanusError);
    assert.equal(err.status, 409);
    assert.match(err.message, /identity provider/);
    return true;
  });
});

test("the catalog needs group:manage, so a read token gets JanusForbiddenError", async () => {
  const { fetch } = fakeFetch({
    "GET /v1/groups?limit=100": () => errorResponse(403, "forbidden", "forbidden"),
  });
  await assert.rejects(client(fetch).listGroups(), (err: unknown) => {
    assert.ok(err instanceof JanusForbiddenError);
    assert.ok(isForbidden(err));
    return true;
  });
});

test("myGroups is authenticated-only and tolerates an empty list", async () => {
  const { fetch, requests } = fakeFetch({
    "GET /v1/auth/me/groups": () => jsonResponse(200, { groups: [] }),
  });
  const groups = await client(fetch).myGroups();
  assert.deepEqual(groups, []);
  assert.equal(requests[0].url, `${BASE}/v1/auth/me/groups`);
});

test("empty identifiers are rejected without a request", async () => {
  const { fetch, requests } = fakeFetch({});
  const c = client(fetch);

  await assert.rejects(() => c.getGroup(""), /groupId is required/);
  await assert.rejects(() => c.deleteGroup(""), /groupId is required/);
  await assert.rejects(() => c.listGroupMembers(""), /groupId is required/);
  await assert.rejects(() => c.setGroupProjectCreation("", true), /groupId is required/);
  await assert.rejects(() => c.addGroupMember(GROUP_ID, ""), /required/);
  await assert.rejects(() => c.removeGroupMember("", USER_ID), /required/);

  assert.equal(requests.length, 0);
});

test("group bindings are deliberately absent from the SDK", () => {
  // Binding a group at a scope is a different authority (`member:manage` there,
  // capped by your own bound role) and a durable grant of access. It belongs in
  // something that plans and diffs — Terraform's janus_group_binding — not in a
  // one-line call from a read-mostly secret client. This test exists so adding
  // one is a deliberate decision rather than a drive-by.
  const surface = Object.getOwnPropertyNames(JanusClient.prototype);
  const bindingish = surface.filter((n) => /bind/i.test(n));
  assert.deepEqual(bindingish, []);
});
