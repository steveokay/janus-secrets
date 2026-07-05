package authz

// Resource is the target of an action plus its scope chain. Any field may be
// empty; an all-empty Resource is instance-scoped.
type Resource struct {
	ProjectID  string
	EnvID      string
	ConfigID   string
	TransitKey string // transit key name (transit ops); empty otherwise
}

// Instance returns the zero-value instance-scoped resource.
func Instance() Resource { return Resource{} }
