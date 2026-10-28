//go:build linux

package mount

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type recordingRunner struct {
	calls [][]string
	fail  map[string]bool
}

func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	if r.fail[name] {
		return []byte(name + " failed"), errors.New("exit status 1")
	}
	return nil, nil
}

func TestUnmountFallsBackToUmount(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fusermount3", "fusermount", "umount"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	mountPoint := filepath.Join(t.TempDir(), "mnt")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &recordingRunner{fail: map[string]bool{"fusermount3": true, "fusermount": true}}
	if err := UnmountWith(context.Background(), mountPoint, r); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 3 {
		t.Fatalf("calls = %#v", r.calls)
	}
	if r.calls[2][0] != "umount" {
		t.Fatalf("last call = %#v", r.calls[2])
	}
}
