package migrations

import "embed"

// FS contains the versioned SQL migrations.
//
//go:embed *.sql
var FS embed.FS
