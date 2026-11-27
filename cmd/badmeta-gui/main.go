// Badmeta-gui presents the shared cleaner through MyGo's native UI.
package main

import (
	"fmt"
	"os"

	"github.com/billstark001/bad-metadata/internal/gui"
)

func main() {
	if err := gui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "badmeta-gui:", err)
		os.Exit(1)
	}
}
