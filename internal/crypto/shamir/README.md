# Vendored: HashiCorp Vault shamir package

Source: https://github.com/hashicorp/vault/tree/v1.15.6/shamir
License: MPL-2.0 (see LICENSE in this directory; original headers retained)

Vendored per project policy (CLAUDE.md): no third-party crypto dependencies
in go.mod, and no hand-rolled crypto primitives. Do not modify these files
except to track upstream.

Note: at tag v1.15.6 upstream does not have a separate `tables.go` — the
GF(256) lookup tables are inlined in `shamir.go`. Only `shamir.go`,
`shamir_test.go`, and `LICENSE` exist in the upstream `shamir/` directory.
