package authz

// Action is a resource:verb permission string.
type Action string

const (
	SecretRead    Action = "secret:read"
	SecretWrite   Action = "secret:write" // set / delete / rollback
	ConfigRead    Action = "config:read"
	ConfigCreate  Action = "config:create"
	ConfigDelete  Action = "config:delete"
	EnvCreate     Action = "env:create"
	EnvDelete     Action = "env:delete"
	ProjectRead   Action = "project:read"
	ProjectCreate Action = "project:create" // instance-scoped
	ProjectDelete Action = "project:delete"
	MemberRead    Action = "member:read"
	MemberManage  Action = "member:manage"
	TokenRead     Action = "token:read"
	TokenMint     Action = "token:mint"
	TokenRevoke   Action = "token:revoke"
	UserManage    Action = "user:manage" // instance-scoped
	AuditRead     Action = "audit:read"
	SysSeal       Action = "sys:seal" // instance-scoped
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
	viewerActions    = setOf(SecretRead, ConfigRead, ProjectRead, MemberRead)
	developerActions = union(viewerActions, setOf(SecretWrite, ConfigCreate))
	adminActions     = union(developerActions, setOf(
		ConfigDelete, EnvCreate, EnvDelete, ProjectCreate, MemberManage,
		TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal))
	ownerActions = union(adminActions, setOf(ProjectDelete))

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
