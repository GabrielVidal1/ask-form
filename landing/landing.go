// Package landing embeds what the relay serves besides the forms: the page
// at / (English) and /fr (French) with their scripts and styles under
// /assets/, and /agent-install (plain-text setup steps for a coding agent).
//
// The pages are built from app/ (React + Vite, `npm run build` there) into
// dist/, which is committed: building the relay needs Go only.
package landing

import (
	"embed"
	"io/fs"
)

// Page is the English landing, served at /.
//
//go:embed dist/index.html
var Page []byte

// PageFR is the French landing, served at /fr. It shares Page's assets.
//
//go:embed dist/fr/index.html
var PageFR []byte

//go:embed dist/assets
var dist embed.FS

// Assets holds the page's hashed script and style files, served at /assets/.
var Assets, _ = fs.Sub(dist, "dist/assets")

//go:embed agent-install.txt
var AgentInstall []byte
