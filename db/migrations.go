package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
