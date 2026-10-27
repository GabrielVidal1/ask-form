// Package web embeds the answer page's templates and static files, shared by
// `ask-form serve` (which renders) and the relay (which serves the statics
// itself).
package web

import "embed"

//go:embed templates/*.html static/*
var FS embed.FS
