package audit

// Actor identifies who performed an action. ID is empty (→ NULL) for an
// anonymous actor (e.g. a failed login); Name is a non-secret email or token
// name and may be empty.
type Actor struct {
	Kind string // "user" | "service_token" | "anonymous"
	ID   string
	Name string
}

// Event is a single auditable action. It has NO value field by construction —
// a secret value can never be recorded.
type Event struct {
	Actor      Actor
	Action     string // e.g. "token.mint"
	Resource   string // path/id; never a value; may be ""
	Detail     string // non-secret specifics ("role=developer"); "" → NULL
	Result     string // "success" | "denied" | "error"
	ResultCode string // envelope code for denied/error; "" → NULL
	IP         string
}
