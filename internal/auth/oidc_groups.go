package auth

import "errors"

// ErrOIDCGroupClaim is returned when the configured group claim is present but
// cannot be interpreted as an authoritative list of group values. It fails the
// login: completing one against a claim we could only partly parse would grant
// access from an authorization input we did not actually understand.
var ErrOIDCGroupClaim = errors.New("auth: unusable OIDC group claim")

// maxGroupsClaimLen bounds the configured claim path. A claim name is an
// identifier, not free text.
const maxGroupsClaimLen = 128

// validGroupsClaim reports whether a configured group-claim path is acceptable:
// empty (sync disabled), or printable non-space ASCII within the length bound.
// Rejecting whitespace and control characters early keeps a mistyped path from
// silently matching nothing, which would look like "nobody is in any group".
func validGroupsClaim(path string) bool {
	if path == "" {
		return true
	}
	if len(path) > maxGroupsClaimLen {
		return false
	}
	if path[0] == '.' || path[len(path)-1] == '.' {
		return false
	}
	for i := 0; i < len(path); i++ {
		if path[i] <= ' ' || path[i] > '~' {
			return false
		}
	}
	return true
}

// maxGroupClaimValues bounds how many groups one token may assert. Rejected,
// not truncated: silently dropping values would look like a legitimate removal
// from those groups and quietly revoke access.
const maxGroupClaimValues = 512

// resolveGroupClaim extracts group values from a verified claim set at the
// configured path (a literal claim name, or a dotted path into nested objects).
//
// The second return value reports whether membership is KNOWN. When false the
// caller must leave the user's snapshot untouched — the difference between
// "this user is in no groups" (clear it) and "this token does not tell us"
// (keep what we had) is the whole safety of the sync:
//
//   - absent claim, no overage marker → known, empty. The user is in no groups.
//     Fails closed (access is lost, never gained) if an operator misconfigures
//     the IdP, which is the right direction to fail.
//   - absent claim, but `_claim_names` names it → NOT known. Entra replaces
//     `groups` with a Microsoft Graph pointer once a user exceeds ~200 groups;
//     reading that as "no groups" would clear every membership the user has and
//     read exactly like a legitimate removal from all of them.
//
// Anything present but uninterpretable is an error, not an empty list.
func resolveGroupClaim(raw map[string]any, path string) ([]string, bool, error) {
	if path == "" {
		return nil, false, nil // sync disabled
	}
	v, present, err := lookupClaim(raw, path)
	if err != nil {
		return nil, false, err
	}
	if !present || v == nil {
		if claimOverflowed(raw, path) {
			return nil, false, nil
		}
		return nil, true, nil
	}
	switch t := v.(type) {
	case string:
		// Some providers emit a lone group unwrapped. Deliberately NOT split on
		// spaces or commas: splitting invents a parse we cannot verify and would
		// break any group whose name contains the delimiter.
		if t == "" {
			return nil, true, nil
		}
		return []string{t}, true, nil
	case []any:
		if len(t) > maxGroupClaimValues {
			return nil, false, ErrOIDCGroupClaim
		}
		out := make([]string, 0, len(t))
		seen := make(map[string]struct{}, len(t))
		for _, el := range t {
			s, ok := el.(string)
			if !ok {
				// A number/object/null among the groups means we do not
				// understand this claim's shape. Skipping the element would
				// silently drop a membership.
				return nil, false, ErrOIDCGroupClaim
			}
			if s == "" {
				continue
			}
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
		return out, true, nil
	default:
		return nil, false, ErrOIDCGroupClaim
	}
}

// lookupClaim resolves a claim path that may be a literal key or a dotted path
// into nested objects. If BOTH constructions exist — a claim literally named
// "a.b" and a nested {"a":{"b":…}} — the path is genuinely ambiguous and is
// rejected rather than resolved by precedence, matching how CI-federation claim
// matching already treats the same collision.
func lookupClaim(raw map[string]any, path string) (any, bool, error) {
	literal, literalOK := raw[path]
	nested, nestedOK := walkClaimPath(raw, path)
	switch {
	case literalOK && nestedOK:
		return nil, false, ErrOIDCGroupClaim
	case literalOK:
		return literal, true, nil
	case nestedOK:
		return nested, true, nil
	default:
		return nil, false, nil
	}
}

// walkClaimPath follows a dotted path through nested objects. It reports false
// unless every segment but the last resolves to an object. A path with no dot
// never resolves here (the literal lookup owns that case), so a top-level claim
// is never reported as ambiguous with itself.
func walkClaimPath(raw map[string]any, path string) (any, bool) {
	head, rest, found := cutDot(path)
	if !found {
		return nil, false
	}
	cur, ok := raw[head].(map[string]any)
	if !ok {
		return nil, false
	}
	for depth := 0; ; depth++ {
		if depth > maxClaimDepth {
			return nil, false
		}
		next, tail, more := cutDot(rest)
		if !more {
			v, ok := cur[rest]
			return v, ok
		}
		cur, ok = cur[next].(map[string]any)
		if !ok {
			return nil, false
		}
		rest = tail
	}
}

// cutDot splits at the first "." (strings.Cut, kept local to avoid an import
// for one call and to make the "no dot" case explicit at every use site).
func cutDot(s string) (head, rest string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// claimOverflowed reports whether the token says the claim exists but was moved
// out of band. Azure AD/Entra emits `_claim_names: {"groups": "src1"}` plus
// `_claim_sources` with a Graph URL instead of the claim itself once the user
// exceeds the group limit (~200 for the code flow, 150 for implicit).
func claimOverflowed(raw map[string]any, path string) bool {
	names, ok := raw["_claim_names"].(map[string]any)
	if !ok {
		return false
	}
	_, named := names[path]
	return named
}
