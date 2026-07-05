// Package resolve composes config inheritance and read-time secret references
// over two ports (RawReader, Authorizer). It is pure: no crypto, no HTTP, no
// store/authz imports. Inheritance is merged first (child wins), then references
// are expanded transitively with cycle detection and a depth cap. Any
// unresolvable reference fails the whole resolution (atomic).
package resolve

import "errors"

var (
	// ErrInheritanceCycle: the inherits_from chain loops.
	ErrInheritanceCycle = errors.New("resolve: inheritance cycle")
	// ErrBrokenInheritance: a base config in the chain is missing or deleted.
	ErrBrokenInheritance = errors.New("resolve: broken inheritance base")
	// ErrReferenceCycle: reference expansion revisits a (config,key) frame.
	ErrReferenceCycle = errors.New("resolve: reference cycle")
	// ErrUnresolvedReference: a referenced project/env/config/key does not exist.
	ErrUnresolvedReference = errors.New("resolve: unresolved reference")
	// ErrForbiddenReference: caller lacks secret:read on a referenced target.
	ErrForbiddenReference = errors.New("resolve: forbidden reference")
	// ErrReferenceDepth: expansion exceeded the depth cap (backstop).
	ErrReferenceDepth = errors.New("resolve: reference depth exceeded")
	// ErrBadReferenceSyntax: a ${...} token is malformed.
	ErrBadReferenceSyntax = errors.New("resolve: bad reference syntax")
)
