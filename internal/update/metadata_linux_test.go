package update

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wallentx/actions-snitch/internal/workflow"
	"golang.org/x/sys/unix"
)

func TestReadOnlyWorkflowRemainsUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the same permission check in Bash")
	}
	root := t.TempDir()
	path := filepath.Join(root, "action.yml")
	original := []byte("uses: owner/action@v1\n")
	writeSnapshot(t, root, "action.yml", original)
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	f, err := workflow.Read(root, "action.yml")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build([]*workflow.File{f}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Commit(context.Background(), root); err == nil {
		t.Fatal("read-only file was replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("read-only source changed")
	}
}

func TestPublicationPreservesExtendedAttributesAndACL(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "action.yml")
	writeSnapshot(t, root, "action.yml", []byte("uses: owner/action@v1\n"))
	attrs := map[string][]byte{"user.actions_snitch_fixture": []byte("preserved metadata"), "system.posix_acl_access": fixtureACL()}
	for name, value := range attrs {
		if err := unix.Setxattr(path, name, value, 0); errors.Is(err, unix.ENOTSUP) {
			t.Skip("filesystem has no xattr/ACL support")
		} else if err != nil {
			t.Fatal(err)
		}
	}
	f, err := workflow.Read(root, "action.yml")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build([]*workflow.File{f}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Commit(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for name, want := range attrs {
		b := make([]byte, 1024)
		n, err := unix.Getxattr(path, name, b)
		if err != nil || !bytes.Equal(b[:n], want) {
			t.Fatalf("attribute %s changed: %x %v", name, b[:n], err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("mode changed: %v %v", info, err)
	}
}

func TestPublicationPreservesInheritedSecurityLabel(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "action.yml")
	f := writeSnapshot(t, root, "action.yml", []byte("uses: owner/action@v1\n"))
	label := make([]byte, 4096)
	n, err := unix.Getxattr(path, "security.selinux", label)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) {
		t.Skip("filesystem does not assign SELinux labels")
	}
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(label[:n])
	plan, err := Build([]*workflow.File{f}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Commit(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	n, err = unix.Getxattr(path, "security.selinux", label)
	if err != nil || !bytes.Equal(label[:n], want) {
		t.Fatalf("security label changed during publication: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "uses: owner/action@v2\n" {
		t.Fatalf("labeled workflow was not updated: %q %v", data, err)
	}
}

func fixtureACL() []byte {
	b := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(b, 2)
	for i, e := range []struct {
		tag, permissions uint16
		id               uint32
	}{{1, 6, 0xffffffff}, {2, 4, 12345}, {4, 4, 0xffffffff}, {16, 4, 0xffffffff}, {32, 0, 0xffffffff}} {
		offset := 4 + i*8
		binary.LittleEndian.PutUint16(b[offset:], e.tag)
		binary.LittleEndian.PutUint16(b[offset+2:], e.permissions)
		binary.LittleEndian.PutUint32(b[offset+4:], e.id)
	}
	return b
}

func TestPublicationDoesNotAcquireDirectoryDefaultACL(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "action.yml")
	f := writeSnapshot(t, root, "action.yml", []byte("uses: owner/action@v1\n"))
	directoryACL := fixtureACL()
	binary.LittleEndian.PutUint16(directoryACL[6:], 7)
	if err := unix.Setxattr(root, "system.posix_acl_default", directoryACL, 0); errors.Is(err, unix.ENOTSUP) {
		t.Skip("filesystem has no ACL support")
	} else if err != nil {
		t.Fatal(err)
	}
	plan, err := Build([]*workflow.File{f}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Commit(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Getxattr(path, "system.posix_acl_access", nil); !errors.Is(err, unix.ENODATA) {
		t.Fatalf("published file gained an inherited ACL: %v", err)
	}
}

func TestInPlaceStrategyRetainsInodeAndACL(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "action.yml")
	writeSnapshot(t, root, "action.yml", []byte("uses: owner/action@v1\n"))
	attrs := map[string][]byte{"user.actions_snitch_fixture": []byte("retained"), "system.posix_acl_access": fixtureACL()}
	for name, value := range attrs {
		if err := unix.Setxattr(path, name, value, 0); errors.Is(err, unix.ENOTSUP) {
			t.Skip("filesystem has no xattr/ACL support")
		} else if err != nil {
			t.Fatal(err)
		}
	}
	f, err := workflow.Read(root, "action.yml")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build([]*workflow.File{f}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.commitInPlace(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !os.SameFile(info, f.Info) {
		t.Fatal("in-place strategy replaced the inode")
	}
	for name, want := range attrs {
		data := make([]byte, 1024)
		n, err := unix.Getxattr(path, name, data)
		if err != nil || !bytes.Equal(data[:n], want) {
			t.Fatalf("in-place attribute %s changed: %v", name, err)
		}
	}
}
