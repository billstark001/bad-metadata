// Package version exposes the single source of release version information.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var number string

func Number() string { return strings.TrimSpace(number) }
