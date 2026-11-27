package cleaner

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func isLink(info fs.FileInfo) bool {
	if info.Mode()&fs.ModeSymlink != 0 {
		return true
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func renameNoReplace(r *os.Root, source, destination string) error {
	from, to, err := openRenameParents(r, source, destination)
	if err != nil {
		return err
	}
	defer from.Close()
	defer to.Close()
	name, err := windows.NewNTUnicodeString(path.Base(source))
	if err != nil {
		return err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(from.Fd()),
		ObjectName:    name,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var status windows.IO_STATUS_BLOCK
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		&attributes, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("refusing to rename a symbolic link or reparse point")
	}
	newName, err := windows.UTF16FromString(path.Base(destination))
	if err != nil {
		return err
	}
	// FILE_RENAME_INFORMATION has a variable-length UTF-16 filename. A zero
	// ReplaceIfExists field makes destination conflict handling atomic.
	type renameInformation struct {
		ReplaceIfExists uint32
		RootDirectory   windows.Handle
		FileNameLength  uint32
		FileName        [1]uint16
	}
	var layout renameInformation
	nameBytes := (len(newName) - 1) * 2
	buffer := make([]byte, int(unsafe.Sizeof(layout))+nameBytes)
	request := (*renameInformation)(unsafe.Pointer(&buffer[0]))
	request.RootDirectory = windows.Handle(to.Fd())
	request.FileNameLength = uint32(nameBytes)
	copy(unsafe.Slice(&request.FileName[0], len(newName)-1), newName)
	return windows.NtSetInformationFile(handle, &status, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
}
