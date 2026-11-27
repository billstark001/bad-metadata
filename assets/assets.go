// Package assets contains the application's shared visual assets.
package assets

import _ "embed"

// IconPNG is the source application icon for native windows.
//
//go:embed icon.png
var IconPNG []byte
