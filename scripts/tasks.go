//go:build ignore

// Portable development and release tasks. Run from the repository root.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

func main() {
	if err := task(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func task() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: go run scripts/tasks.go fmt|fmt-check|version-check|build|package [OS [ARCH]]|resources|race|clean")
	}
	if _, err := os.Stat("internal/version/VERSION"); err != nil {
		return fmt.Errorf("run this task from the repository root: %w", err)
	}
	switch os.Args[1] {
	case "fmt", "fmt-check":
		return format(os.Args[1] == "fmt-check")
	case "version-check":
		_, _, err := releaseInfo()
		return err
	case "race":
		return runGo(map[string]string{"CGO_ENABLED": "1"}, "test", "-race", "./...")
	case "resources":
		return resources()
	case "build":
		return build(runtime.GOOS, runtime.GOARCH, "bin")
	case "package":
		platforms := [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}}
		if len(os.Args) == 3 {
			platforms = [][2]string{{os.Args[2], "amd64"}, {os.Args[2], "arm64"}}
		} else if len(os.Args) == 4 {
			platforms = [][2]string{{os.Args[2], os.Args[3]}}
		} else if len(os.Args) != 2 {
			return fmt.Errorf("package accepts no platform, an OS, or an OS and architecture")
		}
		return packageAll(platforms)
	case "clean":
		for _, name := range []string{"bin", "dist"} {
			absolute, err := filepath.Abs(name)
			if err != nil {
				return err
			}
			root, err := filepath.Abs(".")
			if err != nil || filepath.Dir(absolute) != root || filepath.Base(absolute) != name {
				return fmt.Errorf("refusing to remove unchecked output directory %q", absolute)
			}
			info, err := os.Lstat(absolute)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to remove linked output directory %q", absolute)
			}
			if err := os.RemoveAll(absolute); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown task %q", os.Args[1])
	}
}

func format(check bool) error {
	var files []string
	for _, root := range []string{"assets", "cmd", "internal", "scripts"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	flag := "-w"
	if check {
		flag = "-l"
	}
	tool := filepath.Join(runtime.GOROOT(), "bin", "gofmt")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	command := exec.Command(tool, append([]string{flag}, files...)...)
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return err
	}
	if check && len(output) > 0 {
		return fmt.Errorf("run make fmt on:\n%s", output)
	}
	return nil
}

func releaseInfo() (string, string, error) {
	data, err := os.ReadFile("internal/version/VERSION")
	if err != nil {
		return "", "", err
	}
	version := strings.TrimSpace(string(data))
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(version) {
		return "", "", fmt.Errorf("invalid release version %q", version)
	}
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return "", "", err
	}
	pattern := regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\] - \d{4}-\d{2}-\d{2}\r?$`)
	location := pattern.FindSubmatchIndex(changelog)
	if location == nil || string(changelog[location[2]:location[3]]) != version {
		return "", "", fmt.Errorf("the latest dated CHANGELOG.md entry must match %s", version)
	}
	notes := string(changelog[location[1]:])
	if end := regexp.MustCompile(`(?m)^## `).FindStringIndex(notes); end != nil {
		notes = notes[:end[0]]
	}
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return "", "", fmt.Errorf("release notes for %s are empty", version)
	}
	return version, notes + "\n", nil
}

func runGo(environment map[string]string, args ...string) error {
	tool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	command := exec.Command(tool, args...)
	command.Env = os.Environ()
	for key, value := range environment {
		prefix := key + "="
		for i := len(command.Env) - 1; i >= 0; i-- {
			if strings.HasPrefix(strings.ToUpper(command.Env[i]), strings.ToUpper(prefix)) {
				command.Env = append(command.Env[:i], command.Env[i+1:]...)
			}
		}
		command.Env = append(command.Env, prefix+value)
	}
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func resources() error {
	version, _, err := releaseInfo()
	if err != nil {
		return err
	}
	if err := runGo(nil, "run", "./scripts/icon.go"); err != nil {
		return err
	}
	for _, name := range []string{"cli", "gui"} {
		command := "badmeta"
		if name == "gui" {
			command += "-gui"
		}
		if err := runGo(nil, "run", "github.com/tc-hib/go-winres@v0.3.3", "make",
			"--in", "assets/windows-"+name+".json", "--out", "cmd/"+command+"/rsrc",
			"--arch", "amd64,arm64", "--file-version", version, "--product-version", version); err != nil {
			return err
		}
	}
	return nil
}

func build(goos, arch, directory string) error {
	if goos != "windows" && goos != "darwin" {
		return fmt.Errorf("unsupported OS %q", goos)
	}
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported architecture %q", arch)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	environment := map[string]string{"CGO_ENABLED": "0", "GOOS": goos, "GOARCH": arch}
	for _, name := range []string{"badmeta", "badmeta-gui"} {
		file := name
		if goos == "windows" {
			file += ".exe"
		}
		args := []string{"build", "-trimpath"}
		if goos == "windows" && name == "badmeta-gui" {
			args = append(args, "-ldflags=-H=windowsgui")
		}
		args = append(args, "-o", filepath.Join(directory, file), "./cmd/"+name)
		if err := runGo(environment, args...); err != nil {
			return fmt.Errorf("build %s for %s/%s: %w", name, goos, arch, err)
		}
	}
	return nil
}

func packageAll(platforms [][2]string) error {
	version, notes, err := releaseInfo()
	if err != nil {
		return err
	}
	if err := os.MkdirAll("dist", 0o755); err != nil {
		return err
	}
	var checksums []string
	for _, platform := range platforms {
		goos, arch := platform[0], platform[1]
		directory := filepath.Join("bin", "package", goos+"-"+arch)
		if err := build(goos, arch, directory); err != nil {
			return err
		}
		name := fmt.Sprintf("bad-metadata-v%s-%s-%s.zip", version, goos, arch)
		path := filepath.Join("dist", name)
		if err := archive(path, directory, goos, version); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		checksums = append(checksums, fmt.Sprintf("%x  %s", hash.Sum(nil), name))
		fmt.Println(path)
	}
	sort.Strings(checksums)
	if err := os.WriteFile("dist/SHA256SUMS", []byte(strings.Join(checksums, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile("dist/release-notes.md", []byte(notes), 0o644)
}

func archive(path, directory, goos, version string) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	writer := zip.NewWriter(file)
	defer func() {
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}()
	add := func(name, source string, mode fs.FileMode) error {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return zipBytes(writer, name, data, mode)
	}
	for _, name := range []string{"README.md", "LICENSE", "CHANGELOG.md", "CONTRIBUTING.md", "docs/architecture.md", "assets/icon.png", "assets/icon-prompt.txt"} {
		if err := add(name, name, 0o644); err != nil {
			return err
		}
	}
	if goos == "windows" {
		for _, name := range []string{"badmeta.exe", "badmeta-gui.exe"} {
			if err := add(name, filepath.Join(directory, name), 0o755); err != nil {
				return err
			}
		}
		return nil
	}
	if err := add("badmeta", filepath.Join(directory, "badmeta"), 0o755); err != nil {
		return err
	}
	if err := add("Bad Metadata.app/Contents/MacOS/badmeta-gui", filepath.Join(directory, "badmeta-gui"), 0o755); err != nil {
		return err
	}
	if err := add("Bad Metadata.app/Contents/Resources/icon.icns", "assets/icon.icns", 0o644); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Bad Metadata</string>
<key>CFBundleDisplayName</key><string>Bad Metadata</string>
<key>CFBundleIdentifier</key><string>io.github.billstark001.badmetadata</string>
<key>CFBundleExecutable</key><string>badmeta-gui</string>
<key>CFBundleIconFile</key><string>icon.icns</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>%s</string>
<key>CFBundleVersion</key><string>%s</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
`, version, version)
	return zipBytes(writer, "Bad Metadata.app/Contents/Info.plist", []byte(plist), 0o644)
}

func zipBytes(writer *zip.Writer, name string, data []byte, mode fs.FileMode) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}
