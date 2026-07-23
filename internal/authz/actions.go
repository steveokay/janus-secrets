package authz

// Action is a resource:verb permission string.
type Action string

const (
	SecretRead       Action = "secret:read"
	SecretWrite      Action = "secret:write" // set / delete / rollback
	ConfigRead       Action = "config:read"
	ConfigCreate     Action = "config:create"
	ConfigDelete     Action = "config:delete"
	EnvCreate        Action = "env:create"
	EnvDelete        Action = "env:delete"
	EnvUpdate        Action = "env:update" // rename, admin+ project-scoped
	ProjectRead      Action = "project:read"
	ProjectCreate    Action = "project:create" // instance-scoped
	ProjectUpdate    Action = "project:update" // rename, admin+ project-scoped
	ProjectDelete    Action = "project:delete"
	MemberRead       Action = "member:read"
	MemberManage     Action = "member:manage"
	TokenRead        Action = "token:read"
	TokenMint        Action = "token:mint"
	TokenRevoke      Action = "token:revoke"
	UserManage       Action = "user:manage" // instance-scoped
	AuditRead        Action = "audit:read"
	SysSeal          Action = "sys:seal"          // instance-scoped
	SysBackup        Action = "sys:backup"        // instance-scoped
	TransitRead      Action = "transit:read"      // instance-scoped
	TransitUse       Action = "transit:use"       // instance-scoped
	TransitManage    Action = "transit:manage"    // instance-scoped
	OIDCManage       Action = "oidc:manage"       // instance-scoped
	RotationManage   Action = "rotation:manage"   // project-scoped
	SyncManage       Action = "sync:manage"       // project-scoped
	DynamicManage    Action = "dynamic:manage"    // project-scoped (create/update/delete roles)
	DynamicIssue     Action = "dynamic:issue"     // project-scoped (issue/renew/revoke leases)
	KEKManage        Action = "kek:manage"        // project-scoped, owner-only (rotate/rewrap/status project KEK)
	SysMasterKey     Action = "sys:master-key"    // instance-scoped, owner-only (master-key rotation / rekey)
	SecretPromote    Action = "secret:promote"    // developer+, target-env scoped
	PromotionManage  Action = "promotion:manage"  // admin+, project-scoped (pipeline + locked keys)
	PromotionRequest Action = "promotion:request" // developer+, source-env scoped (approval workflow)
	NotificationManage Action = "notification:manage" // instance-scoped (alerting channels)
)

// Role is a named bundle of actions.
type Role string

const (
	RoleViewer    Role = "viewer"
	RoleDeveloper Role = "developer"
	RoleAdmin     Role = "admin"
	RoleOwner     Role = "owner"
)

func setOf(as ...Action) map[Action]bool {
	m := make(map[Action]bool, len(as))
	for _, a := range as {
		m[a] = true
	}
	return m
}

func union(sets ...map[Action]bool) map[Action]bool {
	out := map[Action]bool{}
	for _, s := range sets {
		for a := range s {
			out[a] = true
		}
	}
	return out
}

// The matrix is built cumulatively: developer ⊇ viewer, admin ⊇ developer, etc.
var (
	viewerActions    = setOf(SecretRead, ConfigRead, ProjectRead, MemberRead, TransitRead)
	developerActions = union(viewerActions, setOf(SecretWrite, ConfigCreate, TransitUse, DynamicIssue, SecretPromote, PromotionRequest))
	adminActions     = union(developerActions, setOf(
		ConfigDelete, EnvCreate, EnvDelete, EnvUpdate, ProjectCreate, ProjectUpdate, MemberManage,
		TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal, SysBackup, TransitManage, OIDCManage, RotationManage, SyncManage, DynamicManage, PromotionManage, NotificationManage))
	ownerActions = union(adminActions, setOf(ProjectDelete, KEKManage, SysMasterKey))

	roleActions = map[Role]map[Action]bool{
		RoleViewer:    viewerActions,
		RoleDeveloper: developerActions,
		RoleAdmin:     adminActions,
		RoleOwner:     ownerActions,
	}
	roleRank = map[Role]int{RoleViewer: 1, RoleDeveloper: 2, RoleAdmin: 3, RoleOwner: 4}
)

// roleAllows reports whether a role's bundle contains an action.
func roleAllows(role Role, action Action) bool { return roleActions[role][action] }

// ValidRole reports whether s names a known role.
func ValidRole(s string) bool { _, ok := roleRank[Role(s)]; return ok }

// RoleAtLeast reports whether role a ranks >= role b.
func RoleAtLeast(a, b Role) bool { return roleRank[a] >= roleRank[b] }

// RoleRankOf returns a role's numeric privilege rank (0 for an unknown role).
// Higher is more privileged: viewer=1 < developer=2 < admin=3 < owner=4.
func RoleRankOf(r Role) int { return roleRank[r] }

// RoleStrictlyAbove reports whether role a ranks strictly higher than role b.
// Used by the break-glass guard: the target must exceed the currently-held role.
func RoleStrictlyAbove(a, b Role) bool { return roleRank[a] > roleRank[b] }
