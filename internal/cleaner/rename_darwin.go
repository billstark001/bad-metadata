package cleaner

import (
	"os"
	"path"

	"golang.org/x/sys/unix"
)

func renameNoReplace(r *os.Root, source, destination string) error {
	from, to, err := openRenameParents(r, source, destination)
	if err != nil {
		return err
	}
	defer from.Close()
	defer to.Close()
	return unix.RenameatxNp(int(from.Fd()), path.Base(source), int(to.Fd()), path.Base(destination), unix.RENAME_EXCL)
}
