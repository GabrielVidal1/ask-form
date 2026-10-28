// Package landing embeds what the relay serves besides the forms: the page
// at / with its scripts and styles under /assets/, and /agent-install
// (plain-text setup steps for a coding agent).
//
// The page is built from app/ (React + Vite, `npm run build` there) into
// dist/, which is committed: building the relay needs Go only.
package landing

import (
	"embed"
	"io/fs"
)

//go:embed dist/index.html
var Page []byte

//go:embed dist/assets
var dist embed.FS

// Assets holds the page's hashed script and style files, served at /assets/.
var Assets, _ = fs.Sub(dist, "dist/assets")

//go:embed agent-install.txt
var AgentInstall []byte
