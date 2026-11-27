package cleaner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeRenameUsesOpenedRootAfterPathReplacement(t *testing.T) {
	base := fixtureRoot(t)
	root := filepath.Join(base, "root")
	moved := filepath.Join(base, "moved-root")
	putFixture(t, root, "source", []byte("original saved data"))
	if err := os.Mkdir(filepath.Join(root, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := os.Rename(root, moved); err != nil {
		t.Skipf("filesystem does not allow renaming an open root: %v", err)
	}
	putFixture(t, root, "source", []byte("replacement user data"))
	if err := os.Mkdir(filepath.Join(root, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := renameNoReplace(r, "source", "target/restored"); err != nil {
		t.Fatal(err)
	}
	assertFixture(t, moved, "target/restored", []byte("original saved data"))
	assertFixture(t, root, "source", []byte("replacement user data"))
	if _, err := os.Stat(filepath.Join(root, "target", "restored")); !os.IsNotExist(err) {
		t.Fatalf("native rename used the replaced root path: %v", err)
	}
}

func TestNativeRenameRejectsEscapingDirectoryLinks(t *testing.T) {
	for _, linkedSide := range []string{"source", "destination"} {
		t.Run(linkedSide, func(t *testing.T) {
			base := fixtureRoot(t)
			root := filepath.Join(base, "root")
			outside := filepath.Join(base, "outside")
			putFixture(t, root, "source", []byte("inside saved data"))
			putFixture(t, outside, "source", []byte("outside user data"))
			link := filepath.Join(root, "escape")
			if err := os.Symlink(outside, link); err != nil {
				if runtime.GOOS != "windows" {
					t.Skipf("symbolic links unavailable: %v", err)
				}
				quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
				command := "New-Item -ItemType Junction -Path " + quote(link) + " -Target " + quote(outside) + " | Out-Null"
				if output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput(); err != nil {
					t.Skipf("junctions unavailable: %v: %s", err, output)
				}
			}
			r, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			source, destination := "source", "escape/destination"
			if linkedSide == "source" {
				source, destination = "escape/source", "destination"
			}
			if err := renameNoReplace(r, source, destination); err == nil {
				t.Fatal("native rename accepted a parent link escaping the root")
			}
			assertFixture(t, root, "source", []byte("inside saved data"))
			assertFixture(t, outside, "source", []byte("outside user data"))
			if _, err := os.Stat(filepath.Join(outside, "destination")); !os.IsNotExist(err) {
				t.Fatalf("native rename wrote outside the opened root: %v", err)
			}
		})
	}
}
