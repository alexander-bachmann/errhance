//go:build windows

package errhance

import (
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fileMetadata struct {
	sd                 *windows.SECURITY_DESCRIPTOR
	owner, group, dacl string
	protected          bool
	attributes         uint32
}

var queryWindowsFileInfo = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryInformationFile")

func checkFileLinks(path string, _ os.FileInfo) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("unsafe hard link or reparse point: %s", path)
	}
	return nil
}

// Preserve owner/group, DACL (including ACE order and protection), and ordinary
// file attributes. Reject streams, EAs, explicit mandatory/dynamic access labels and special
// file attributes. Privileged audit SACLs are not queried or migrated; this is
// an access-control policy, not a privileged backup/security migration tool.
func readFileMetadata(path string, original os.FileInfo) (fileMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileMetadata{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fileMetadata{}, err
	}
	if !os.SameFile(original, info) {
		return fileMetadata{}, fmt.Errorf("file identity changed: %s", path)
	}
	return windowsFileMetadata(windows.Handle(file.Fd()))
}

func windowsFileMetadata(handle windows.Handle) (fileMetadata, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fileMetadata{}, err
	}
	const supported = windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_ATTRIBUTE_ARCHIVE | windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_SYSTEM | windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED
	if info.NumberOfLinks != 1 || info.FileAttributes & ^uint32(supported) != 0 {
		return fileMetadata{}, fmt.Errorf("unsupported Windows file attributes or hard links: %#x", info.FileAttributes)
	}
	// FileStreamInfo consists of chained FILE_STREAM_INFO records. An ordinary
	// source file has exactly one unnamed ::$DATA stream; never drop an ADS.
	streams := make([]byte, 64*1024)
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileStreamInfo, &streams[0], uint32(len(streams))); err != nil {
		return fileMetadata{}, fmt.Errorf("inspect Windows streams: %w", err)
	}
	nameLength := binary.LittleEndian.Uint32(streams[4:8])
	if binary.LittleEndian.Uint32(streams[:4]) != 0 || nameLength != 14 || string(streams[24:38]) != ":\x00:\x00$\x00D\x00A\x00T\x00A\x00" {
		return fileMetadata{}, fmt.Errorf("unsupported Windows alternate data streams")
	}
	var iosb windows.IO_STATUS_BLOCK
	var eaSize uint32
	// os.Open/os.CreateTemp supply synchronous handles. Require a complete
	// FILE_EA_INFORMATION result, not merely a nonnegative NTSTATUS.
	status, _, _ := queryWindowsFileInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&iosb)), uintptr(unsafe.Pointer(&eaSize)), unsafe.Sizeof(eaSize), 7) // FileEaInformation
	if status != 0 {
		return fileMetadata{}, fmt.Errorf("inspect Windows extended attributes: %w", windows.NTStatus(status))
	}
	if iosb.Information != unsafe.Sizeof(eaSize) {
		return fileMetadata{}, fmt.Errorf("incomplete Windows extended attribute information")
	}
	if eaSize != 0 {
		return fileMetadata{}, fmt.Errorf("unsupported Windows extended attributes")
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION|windows.ATTRIBUTE_SECURITY_INFORMATION|windows.SCOPE_SECURITY_INFORMATION)
	if err != nil {
		return fileMetadata{}, err
	}
	sacl, _, err := sd.SACL()
	if err != nil && err != windows.ERROR_OBJECT_NOT_FOUND {
		return fileMetadata{}, err
	}
	if sacl != nil && sacl.AceCount != 0 {
		return fileMetadata{}, fmt.Errorf("unsupported Windows mandatory or dynamic access label")
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fileMetadata{}, err
	}
	group, _, err := sd.Group()
	if err != nil {
		return fileMetadata{}, err
	}
	if owner == nil || group == nil {
		return fileMetadata{}, fmt.Errorf("missing Windows owner/group")
	}
	control, _, err := sd.Control()
	if err != nil {
		return fileMetadata{}, err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return fileMetadata{}, fmt.Errorf("unsupported absent Windows DACL: %w", err)
	}
	// Compare ACE bytes rather than SDDL control bookkeeping such as
	// AUTO_INHERITED, which SetSecurityInfo can update without changing access.
	dacl := "null"
	if acl != nil {
		dacl = "present"
		for i := uint32(0); i < uint32(acl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, i, &ace); err != nil {
				return fileMetadata{}, err
			}
			header := unsafe.Slice((*byte)(unsafe.Pointer(ace)), 4)
			size := binary.LittleEndian.Uint16(header[2:4])
			if size < 4 {
				return fileMetadata{}, fmt.Errorf("invalid Windows ACE")
			}
			dacl += string(unsafe.Slice((*byte)(unsafe.Pointer(ace)), int(size)))
		}
	}
	return fileMetadata{sd: sd, owner: owner.String(), group: group.String(), dacl: dacl, protected: control&windows.SE_DACL_PROTECTED != 0, attributes: info.FileAttributes}, nil
}

func sameFileMetadata(a, b fileMetadata) bool {
	return a.owner == b.owner && a.group == b.group && a.dacl == b.dacl && a.protected == b.protected && a.attributes == b.attributes
}

func applyFileMetadata(file *os.File, original fileMetadata, mode os.FileMode) error {
	current, err := windowsFileMetadata(windows.Handle(file.Fd()))
	if err != nil {
		return err
	}
	owner, _, err := original.sd.Owner()
	if err != nil {
		return err
	}
	group, _, err := original.sd.Group()
	if err != nil {
		return err
	}
	dacl, _, err := original.sd.DACL()
	if err != nil {
		return err
	}
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if original.protected {
		flags |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		flags |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	// Avoid requiring WRITE_OWNER when the temp already has the correct owner
	// and group. Never enable take-ownership or restore privileges to force it.
	if current.owner != original.owner {
		flags |= windows.OWNER_SECURITY_INFORMATION
	}
	if current.group != original.group {
		flags |= windows.GROUP_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(file.Name(), windows.SE_FILE_OBJECT, flags, owner, group, dacl, nil); err != nil {
		return fmt.Errorf("preserve Windows security: %w", err)
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	path, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return err
	}
	if err := windows.SetFileAttributes(path, original.attributes); err != nil {
		return err
	}
	after, err := windowsFileMetadata(windows.Handle(file.Fd()))
	if err != nil {
		return err
	}
	if !sameFileMetadata(original, after) {
		return fmt.Errorf("cannot preserve Windows access metadata of %s", file.Name())
	}
	return nil
}
