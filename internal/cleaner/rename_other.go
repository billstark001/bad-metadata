//go:build !windows && !darwin && !linux

package cleaner

import (
	"errors"
	"os"
)

func renameNoReplace(r *os.Root, source, destination string) error {
	return errors.New("atomic restore is only supported on Windows, macOS and Linux")
}
