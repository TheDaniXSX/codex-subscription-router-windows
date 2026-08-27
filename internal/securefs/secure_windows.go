package securefs

import (
	"fmt"
	"os/user"
	"strings"
	"syscall"
	"unsafe"
)

const (
	localSystemSID                         = "S-1-5-18"
	seFileObject                           = 1
	ownerSecurityInformation               = 0x00000001
	daclSecurityInformation                = 0x00000004
	protectedDACLInformation               = 0x80000000
	aclRevision                            = 2
	aclSizeInformationClass                = 2
	accessAllowedACEType                   = 0
	objectInheritACE                       = 0x01
	containerInheritACE                    = 0x02
	inheritedACEFlag                       = 0x10
	securityDescriptorDACLProtected        = 0x1000
	fileAllAccess                   uint32 = 0x001f01ff
)

var (
	advapi32                     = syscall.NewLazyDLL("advapi32.dll")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	getNamedSecurityInfoW        = advapi32.NewProc("GetNamedSecurityInfoW")
	setNamedSecurityInfoW        = advapi32.NewProc("SetNamedSecurityInfoW")
	getAclInformation            = advapi32.NewProc("GetAclInformation")
	getAce                       = advapi32.NewProc("GetAce")
	initializeAcl                = advapi32.NewProc("InitializeAcl")
	addAccessAllowedAceEx        = advapi32.NewProc("AddAccessAllowedAceEx")
	convertStringSidToSidW       = advapi32.NewProc("ConvertStringSidToSidW")
	convertSidToStringSidW       = advapi32.NewProc("ConvertSidToStringSidW")
	getLengthSid                 = advapi32.NewProc("GetLengthSid")
	equalSid                     = advapi32.NewProc("EqualSid")
	getSecurityDescriptorControl = advapi32.NewProc("GetSecurityDescriptorControl")
	getFileAttributesW           = kernel32.NewProc("GetFileAttributesW")
	localFree                    = kernel32.NewProc("LocalFree")
)

type aclSizeInformation struct {
	AceCount      uint32
	AclBytesInUse uint32
	AclBytesFree  uint32
}

type aceHeader struct {
	Type  byte
	Flags byte
	Size  uint16
}

type accessAllowedACE struct {
	Header   aceHeader
	Mask     uint32
	SidStart uint32
}

type allowedACERecord struct {
	index uint32
	type_ byte
	flags byte
	mask  uint32
	sid   string
}

type securitySnapshot struct {
	descriptor unsafe.Pointer
	owner      unsafe.Pointer
	dacl       unsafe.Pointer
}

func PrivateDirectory(path string) error {
	if err := validateWindowsObject(path, true); err != nil {
		return err
	}
	return restrictACL(path, objectInheritACE|containerInheritACE)
}

func PrivateFile(path string) error {
	if err := validateWindowsObject(path, false); err != nil {
		return err
	}
	return restrictACL(path, 0)
}

func validateWindowsObject(path string, wantDirectory bool) error {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, _, callErr := getFileAttributesW.Call(uintptr(unsafe.Pointer(pointer)))
	if attributes == 0xffffffff {
		return fmt.Errorf("inspect private object %q: %w", path, callErr)
	}
	const (
		fileAttributeDirectory    = 0x00000010
		fileAttributeReparsePoint = 0x00000400
	)
	if attributes&fileAttributeReparsePoint != 0 {
		return fmt.Errorf("private object %q is a reparse point", path)
	}
	isDirectory := attributes&fileAttributeDirectory != 0
	if isDirectory != wantDirectory {
		if wantDirectory {
			return fmt.Errorf("private object %q is not a directory", path)
		}
		return fmt.Errorf("private object %q is not a regular file", path)
	}
	return nil
}

// restrictACL replaces the DACL instead of incrementally editing inherited
// entries. The resulting descriptor has exactly two non-inherited allow ACEs:
// the current user and LocalSystem, both with FILE_ALL_ACCESS. It also pins the
// owner to the current user so a pre-existing hostile owner cannot rewrite the
// DACL after validation.
func restrictACL(path string, inheritanceFlags byte) error {
	currentUserSID, err := currentSID()
	if err != nil {
		return err
	}
	userSID, freeUserSID, err := parseSID(currentUserSID)
	if err != nil {
		return fmt.Errorf("decode current Windows user SID: %w", err)
	}
	defer freeUserSID()
	systemSID, freeSystemSID, err := parseSID(localSystemSID)
	if err != nil {
		return fmt.Errorf("decode LocalSystem SID: %w", err)
	}
	defer freeSystemSID()

	acl, err := buildPrivateACL(userSID, systemSID, inheritanceFlags)
	if err != nil {
		return fmt.Errorf("build private ACL for %q: %w", path, err)
	}
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	result, _, _ := setNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		seFileObject,
		ownerSecurityInformation|daclSecurityInformation|protectedDACLInformation,
		uintptr(userSID),
		0,
		uintptr(unsafe.Pointer(&acl[0])),
		0,
	)
	if result != 0 {
		return fmt.Errorf("set private owner and DACL for %q: %w", path, syscall.Errno(result))
	}
	if err := verifyRestrictedACL(path, currentUserSID, inheritanceFlags); err != nil {
		return fmt.Errorf("verify private ACL for %q: %w", path, err)
	}
	return nil
}

func currentSID() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current Windows user: %w", err)
	}
	sid := strings.TrimSpace(current.Uid)
	if sid == "" {
		return "", fmt.Errorf("current Windows user has no SID")
	}
	return sid, nil
}

func parseSID(value string) (unsafe.Pointer, func(), error) {
	encoded, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return nil, func() {}, err
	}
	var sid unsafe.Pointer
	ok, _, callErr := convertStringSidToSidW.Call(
		uintptr(unsafe.Pointer(encoded)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if ok == 0 {
		return nil, func() {}, callErr
	}
	return sid, func() { _, _, _ = localFree.Call(uintptr(sid)) }, nil
}

func buildPrivateACL(userSID, systemSID unsafe.Pointer, flags byte) ([]byte, error) {
	userLength, _, userErr := getLengthSid.Call(uintptr(userSID))
	if userLength == 0 {
		return nil, userErr
	}
	systemLength, _, systemErr := getLengthSid.Call(uintptr(systemSID))
	if systemLength == 0 {
		return nil, systemErr
	}
	// ACCESS_ALLOWED_ACE includes a four-byte SidStart placeholder, hence each
	// variable ACE consumes eight fixed bytes plus the complete SID.
	size := uintptr(8) + (8 + userLength) + (8 + systemLength)
	acl := make([]byte, size)
	ok, _, callErr := initializeAcl.Call(uintptr(unsafe.Pointer(&acl[0])), size, aclRevision)
	if ok == 0 {
		return nil, callErr
	}
	for _, sid := range []unsafe.Pointer{userSID, systemSID} {
		ok, _, callErr = addAccessAllowedAceEx.Call(
			uintptr(unsafe.Pointer(&acl[0])),
			aclRevision,
			uintptr(flags),
			uintptr(fileAllAccess),
			uintptr(sid),
		)
		if ok == 0 {
			return nil, callErr
		}
	}
	return acl, nil
}

func readSecurity(path string) (securitySnapshot, error) {
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return securitySnapshot{}, err
	}
	var snapshot securitySnapshot
	result, _, _ := getNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		seFileObject,
		ownerSecurityInformation|daclSecurityInformation,
		uintptr(unsafe.Pointer(&snapshot.owner)),
		0,
		uintptr(unsafe.Pointer(&snapshot.dacl)),
		0,
		uintptr(unsafe.Pointer(&snapshot.descriptor)),
	)
	if result != 0 {
		return securitySnapshot{}, syscall.Errno(result)
	}
	if snapshot.descriptor == nil {
		return securitySnapshot{}, fmt.Errorf("object has no security descriptor")
	}
	if snapshot.owner == nil {
		snapshot.close()
		return securitySnapshot{}, fmt.Errorf("object has no owner SID")
	}
	if snapshot.dacl == nil {
		snapshot.close()
		return securitySnapshot{}, fmt.Errorf("object has a null DACL")
	}
	return snapshot, nil
}

func (snapshot *securitySnapshot) close() {
	if snapshot.descriptor != nil {
		_, _, _ = localFree.Call(uintptr(snapshot.descriptor))
		snapshot.descriptor = nil
	}
}

func readAllowedACEs(dacl unsafe.Pointer) ([]allowedACERecord, error) {
	info := aclSizeInformation{}
	ok, _, callErr := getAclInformation.Call(
		uintptr(dacl),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
		aclSizeInformationClass,
	)
	if ok == 0 {
		return nil, fmt.Errorf("read ACL information: %w", callErr)
	}
	records := make([]allowedACERecord, 0, info.AceCount)
	for index := uint32(0); index < info.AceCount; index++ {
		var acePointer unsafe.Pointer
		ok, _, callErr = getAce.Call(uintptr(dacl), uintptr(index), uintptr(unsafe.Pointer(&acePointer)))
		if ok == 0 {
			return nil, fmt.Errorf("read ACE %d: %w", index, callErr)
		}
		header := (*aceHeader)(acePointer)
		record := allowedACERecord{index: index, type_: header.Type, flags: header.Flags}
		if header.Type == accessAllowedACEType {
			accessACE := (*accessAllowedACE)(acePointer)
			record.mask = accessACE.Mask
			sid, err := sidToString(unsafe.Pointer(&accessACE.SidStart))
			if err != nil {
				return nil, fmt.Errorf("decode ACE %d SID: %w", index, err)
			}
			record.sid = sid
		}
		records = append(records, record)
	}
	return records, nil
}

func verifyRestrictedACL(path, currentUserSID string, expectedFlags byte) error {
	snapshot, err := readSecurity(path)
	if err != nil {
		return err
	}
	defer snapshot.close()
	expectedOwner, freeExpectedOwner, err := parseSID(currentUserSID)
	if err != nil {
		return err
	}
	defer freeExpectedOwner()
	equal, _, _ := equalSid.Call(uintptr(snapshot.owner), uintptr(expectedOwner))
	if equal == 0 {
		owner, ownerErr := sidToString(snapshot.owner)
		if ownerErr != nil {
			owner = "<unreadable>"
		}
		return fmt.Errorf("owner SID is %s, want %s", owner, currentUserSID)
	}

	var control uint16
	var revision uint32
	ok, _, callErr := getSecurityDescriptorControl.Call(
		uintptr(snapshot.descriptor),
		uintptr(unsafe.Pointer(&control)),
		uintptr(unsafe.Pointer(&revision)),
	)
	if ok == 0 {
		return fmt.Errorf("read security descriptor control: %w", callErr)
	}
	if control&securityDescriptorDACLProtected == 0 {
		return fmt.Errorf("DACL inheritance is not protected")
	}

	records, err := readAllowedACEs(snapshot.dacl)
	if err != nil {
		return err
	}
	if len(records) != 2 {
		return fmt.Errorf("DACL has %d ACEs, want exactly 2", len(records))
	}
	expected := map[string]bool{currentUserSID: false, localSystemSID: false}
	for _, record := range records {
		if record.type_ != accessAllowedACEType {
			return fmt.Errorf("ACE %d has type %d, want ACCESS_ALLOWED_ACE", record.index, record.type_)
		}
		if record.flags&inheritedACEFlag != 0 {
			return fmt.Errorf("ACE %d still inherits access", record.index)
		}
		if record.flags != expectedFlags {
			return fmt.Errorf("ACE %d flags are %#x, want %#x", record.index, record.flags, expectedFlags)
		}
		if record.mask != fileAllAccess {
			return fmt.Errorf("ACE %d access mask is %#x, want %#x", record.index, record.mask, fileAllAccess)
		}
		if _, ok := expected[record.sid]; !ok {
			return fmt.Errorf("ACE %d grants unexpected SID %s", record.index, record.sid)
		}
		if expected[record.sid] {
			return fmt.Errorf("SID %s has duplicate allow ACEs", record.sid)
		}
		expected[record.sid] = true
	}
	for sid, found := range expected {
		if !found {
			return fmt.Errorf("required SID %s has no allow ACE", sid)
		}
	}
	return nil
}

func sidToString(sid unsafe.Pointer) (string, error) {
	var encoded unsafe.Pointer
	ok, _, callErr := convertSidToStringSidW.Call(uintptr(sid), uintptr(unsafe.Pointer(&encoded)))
	if ok == 0 {
		return "", callErr
	}
	defer localFree.Call(uintptr(encoded))
	pointer := (*uint16)(encoded)
	length := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(pointer), uintptr(length)*unsafe.Sizeof(*pointer))) != 0 {
		length++
	}
	return syscall.UTF16ToString(unsafe.Slice(pointer, length)), nil
}
