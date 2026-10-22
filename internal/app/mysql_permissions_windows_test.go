package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMySQLWindowsDirectoryAndFileACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := restrictMySQLDirectory(dir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "client.cnf")
	if err := os.WriteFile(file, []byte("test credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, file} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		acl := sd.String()
		for _, broad := range []string{";;;WD)", ";;;BU)", ";;;AU)", ";;;BA)"} {
			if strings.Contains(acl, broad) {
				t.Fatal("broad ACL inherited", acl)
			}
		}
		if path == dir {
			control, _, err := sd.Control()
			if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
				t.Fatal("directory inheritance not disabled", err, acl)
			}
		}
	}
}
