// Package migrations embeds the SQL migration files.
package migrations

import "embed"

// FS holds *.up.sql / *.down.sql files next to this file.
//
//go:embed *.up.sql *.down.sql
var FS embed.FS
