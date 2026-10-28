package mount

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMountInfo(t *testing.T) {
	data := `36 35 98:0 /mnt1 /mnt/with\040space rw,noatime master:1 - ext3 /dev/root rw
37 35 98:1 / / rw,noatime - overlay overlay rw
`
	if !parseMountInfo(data, "/mnt/with space") {
		t.Fatal("escaped mount point not detected")
	}
	if !parseMountInfo(data, "/") {
		t.Fatal("root mount not detected")
	}
	if parseMountInfo(data, "/absent") {
		t.Fatal("false positive")
	}
}

func TestParseProcMounts(t *testing.T) {
	data := `/dev/sda1 /mnt/with\040space ext4 rw 0 0
proc /proc proc rw 0 0
`
	if !parseProcMounts(data, "/mnt/with space") {
		t.Fatal("escaped mount point not detected")
	}
}

func TestValidateMountPoint(t *testing.T) {
	if _, err := ValidateMountPoint("/"); err == nil {
		t.Fatal("root must be rejected")
	}
	home, _ := os.UserHomeDir()
	if _, err := ValidateMountPoint(home); err == nil {
		t.Fatal("home must be rejected")
	}
	dir := t.TempDir()
	got, err := ValidateMountPoint(filepath.Join(dir, "mnt"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "mnt") {
		t.Fatalf("path = %q", got)
	}
	if _, err := ValidateMountPoint(""); err == nil {
		t.Fatal("empty path must be rejected")
	}
}

func TestEnsureMountPoint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	got, err := EnsureMountPoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(got)
	if err != nil || !info.IsDir() {
		t.Fatalf("mount point not created: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMountPoint(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected not-a-directory error, got %v", err)
	}
}
