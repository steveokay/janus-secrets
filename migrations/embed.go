// Package migrations embeds the SQL migration files so they ship inside the
// binary. The store's migrate runner reads them via this FS.
package migrations

import "embed"

// FS holds all *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
