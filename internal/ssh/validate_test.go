package ssh

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFakeSSH(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake ssh is not supported on Windows")
	}
	dir := t.TempDir()
	ok := writeFakeSSH(t, dir, "exit 0\n")
	if err := CheckConfig(context.Background(), ok, filepath.Join(dir, "config"), "alpha"); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	bad := writeFakeSSH(t, dir, `echo "Pseudo-terminal will not be allocated because stdin is not a terminal." >&2
echo "/tmp/config: line 2: Bad configuration option: Nope" >&2
exit 1
`)
	err := CheckConfig(context.Background(), bad, filepath.Join(dir, "config"), "alpha")
	if err == nil {
		t.Fatal("expected diagnostic error")
	}
	if !strings.Contains(err.Error(), "Bad configuration option: Nope") {
		t.Fatalf("diagnostic missing: %v", err)
	}
	if strings.Contains(err.Error(), "Pseudo-terminal") {
		t.Fatalf("pseudo-terminal warning should be ignored: %v", err)
	}
}

func TestCheckConfigRejectsBadArguments(t *testing.T) {
	if err := CheckConfig(context.Background(), "ssh", "", "alpha"); err == nil {
		t.Fatal("empty config path should fail")
	}
	if err := CheckConfig(context.Background(), "ssh", "/tmp/config", "-oProxyCommand=bad"); err == nil {
		t.Fatal("option-like alias should fail")
	}
}
