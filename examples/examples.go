// Package examples embeds the demo forms shown at /examples: one directory
// per example, a spec.json (title, summary, description, order, fields — the
// same field spec `ask-form create` takes) and the images it references as
// {{asset:<file>}}.
package examples

import "embed"

//go:embed */*
var FS embed.FS
