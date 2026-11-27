// Package examples embeds the demo forms shown at /examples: one directory
// per example, a spec.yaml (the form file `ask-form create` takes — title,
// description, sections — plus an example's summary, order and multi), its
// French twin spec.fr.yaml, and the images it references as {{asset:<file>}}.
package examples

import "embed"

//go:embed */*
var FS embed.FS
