"""Groups for the Janus Python SDK.

A **group** is a subject a role binding can target instead of a user, so a
whole team is granted access once instead of per person.

Everything here is part of the instance group **catalog** and needs
``group:manage`` (admin or owner). A config- or environment-scoped
``janus_svc_...`` read token — the usual credential for this SDK — is refused
with :class:`~janus_client.errors.Forbidden` by all of it except
:meth:`~janus_client.client.Client.my_groups`.

Group *bindings* (granting a group a role at a scope) are deliberately absent
from this SDK: they are a different authority (``member:manage`` at that scope,
capped by your own bound role) and they grant durable access, which belongs in
something that plans and diffs. Use the Terraform ``janus_group_binding``
resource, ``janus group bind``, or the UI. See ``docs/guides/python-sdk.md``.
"""

from __future__ import annotations

from typing import Any, Dict, Mapping, Optional

#: ``oidc`` — membership comes from the identity provider's group claim,
#: refreshed at each sign-in.
GROUP_KIND_OIDC = "oidc"

#: ``local`` — membership is an explicit list managed in Janus.
GROUP_KIND_LOCAL = "local"


class Group:
    """A Janus group.

    Attributes:
        id: Group UUID.
        name: Group name, unique across BOTH kinds — so an IdP group and a
            local group can never quietly become the same group.
        kind: :data:`GROUP_KIND_OIDC` or :data:`GROUP_KIND_LOCAL`. A group is
            one or the other and never both; the split is enforced in the
            database schema, not just at the API, which is what makes "access
            granted through an IdP group is fully described by the IdP" a
            statement you can rely on during an access review.
        claim_value: The exact value the identity provider emits for this
            group. Set only for ``oidc`` groups; ``None`` otherwise.
        description: Display material, never secret material.
        can_create_projects: The narrow delegated project-creation capability.
            Deliberately a capability rather than a role: any role carrying
            ``project:create`` at instance scope would also carry
            ``project:read`` there, revealing every project.
        members_seen: How many users Janus has **recorded** in this group.
            Deliberately not named ``member_count``: for an ``oidc`` group,
            membership is a snapshot refreshed at each sign-in, so this counts
            only users who have actually signed in — never the identity
            provider's membership list. Do not present it as the size of the
            team.
        binding_count: How many scopes the group is bound at.
        created_at: Creation timestamp as returned by the server (RFC 3339).
    """

    __slots__ = (
        "id",
        "name",
        "kind",
        "claim_value",
        "description",
        "can_create_projects",
        "members_seen",
        "binding_count",
        "created_at",
    )

    def __init__(
        self,
        id: str,  # noqa: A002 - mirrors the API field name
        name: str = "",
        kind: str = GROUP_KIND_LOCAL,
        claim_value: Optional[str] = None,
        description: str = "",
        can_create_projects: bool = False,
        members_seen: int = 0,
        binding_count: int = 0,
        created_at: str = "",
    ) -> None:
        self.id = id
        self.name = name
        self.kind = kind
        self.claim_value = claim_value
        self.description = description
        self.can_create_projects = can_create_projects
        self.members_seen = members_seen
        self.binding_count = binding_count
        self.created_at = created_at

    @classmethod
    def _from_wire(cls, raw: Mapping[str, Any]) -> "Group":
        claim = raw.get("claim_value")
        return cls(
            id=str(raw.get("id", "")),
            name=str(raw.get("name", "")),
            kind=str(raw.get("kind", GROUP_KIND_LOCAL)),
            claim_value=str(claim) if claim else None,
            description=str(raw.get("description", "") or ""),
            can_create_projects=bool(raw.get("can_create_projects", False)),
            members_seen=int(raw.get("member_count", 0) or 0),
            binding_count=int(raw.get("binding_count", 0) or 0),
            created_at=str(raw.get("created_at", "") or ""),
        )

    def __repr__(self) -> str:
        return "Group(id=%r, name=%r, kind=%r)" % (self.id, self.name, self.kind)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Group):
            return NotImplemented
        return all(getattr(self, f) == getattr(other, f) for f in self.__slots__)


class GroupMember:
    """One recorded membership row.

    For an ``oidc`` group, a list of these covers only users who have signed
    in: a member the IdP knows about who has never logged into Janus does not
    appear, because Janus has never seen a token for them.

    Attributes:
        user_id: Janus user UUID.
        added_at: When the membership row was created — when an admin added the
            user for a ``local`` group, or when a login sync first recorded
            them for an ``oidc`` one.
    """

    __slots__ = ("user_id", "added_at")

    def __init__(self, user_id: str, added_at: str = "") -> None:
        self.user_id = user_id
        self.added_at = added_at

    @classmethod
    def _from_wire(cls, raw: Mapping[str, Any]) -> "GroupMember":
        return cls(
            user_id=str(raw.get("user_id", "")),
            added_at=str(raw.get("created_at", "") or ""),
        )

    def __repr__(self) -> str:
        return "GroupMember(user_id=%r)" % (self.user_id,)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, GroupMember):
            return NotImplemented
        return self.user_id == other.user_id and self.added_at == other.added_at


def build_group_body(
    name: str,
    kind: str,
    claim_value: Optional[str] = None,
    description: str = "",
    can_create_projects: bool = False,
) -> Dict[str, Any]:
    """Validate the two-kinds rule and build the create body.

    A ``local`` group has an explicit member list and no claim; an ``oidc``
    group is defined BY its claim value and must carry one (an empty claim would
    match a group nothing can ever assert). Enforced here so an impossible group
    never costs a round trip.

    Raises:
        ValueError: if the name is empty or the kind/claim pairing is invalid.
    """
    if not name:
        raise ValueError("janus: group name is required")
    body: Dict[str, Any] = {"name": name, "kind": kind}
    if kind == GROUP_KIND_LOCAL:
        if claim_value:
            raise ValueError(
                "janus: a local group cannot have a claim value; "
                "its membership is the explicit member list"
            )
    elif kind == GROUP_KIND_OIDC:
        if not claim_value:
            raise ValueError(
                "janus: an oidc group requires a claim value; "
                "without one it matches nothing a token can assert"
            )
        body["claim_value"] = claim_value
    else:
        raise ValueError(
            "janus: group kind must be %r or %r, got %r"
            % (GROUP_KIND_OIDC, GROUP_KIND_LOCAL, kind)
        )
    if description:
        body["description"] = description
    if can_create_projects:
        body["can_create_projects"] = True
    return body


__all__ = [
    "Group",
    "GroupMember",
    "GROUP_KIND_OIDC",
    "GROUP_KIND_LOCAL",
    "build_group_body",
]
