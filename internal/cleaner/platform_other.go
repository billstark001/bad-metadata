//go:build !windows

package cleaner

import "io/fs"

func isLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }
