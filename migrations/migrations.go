// Package migrations embeds the golang-migrate SQL files so the binary carries its schema.
// A change is a new numbered pair; an existing migration is never edited.
package migrations

import "embed"

// FS holds the NNNN_name.up.sql / NNNN_name.down.sql files.
//
//go:embed *.sql
var FS embed.FS
