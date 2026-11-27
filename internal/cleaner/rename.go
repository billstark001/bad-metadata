package cleaner

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// openRenameParents resolves both directories through the existing Root before
// exposing their handles to platform APIs. Retaining those handles prevents a
// concurrent path rename or link replacement from redirecting the native call.
func openRenameParents(r *os.Root, source, destination string) (*os.File, *os.File, error) {
	for _, name := range []string{source, destination} {
		if !filepath.IsLocal(filepath.FromSlash(name)) || name == "." ||
			path.Clean(name) != name || strings.Contains(name, "\\") {
			return nil, nil, fmt.Errorf("unsafe rename path %q", name)
		}
	}
	openParent := func(name string) (*os.File, error) {
		parent, err := r.OpenRoot(path.Dir(name))
		if err != nil {
			return nil, err
		}
		defer parent.Close()
		return parent.Open(".")
	}
	from, err := openParent(source)
	if err != nil {
		return nil, nil, err
	}
	to, err := openParent(destination)
	if err != nil {
		_ = from.Close()
		return nil, nil, err
	}
	return from, to, nil
}
