// Package askform holds the files published next to the binaries.
//
// install.sh lives at the repository root, so a raw GitHub URL works as well
// as https://forms.gabvdl.xyz/install.sh, which the relay serves from here.
package askform

import _ "embed"

//go:embed install.sh
var InstallScript []byte
