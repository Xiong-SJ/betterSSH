package ssh

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"betterSSH/internal/keyring"
)

type fakeStore struct {
	password string
	err      error
	gotKey   string
}

func (f *fakeStore) Set(key, password string) error { f.gotKey = key; return nil }
func (f *fakeStore) Get(key string) (string, error) { f.gotKey = key; return f.password, f.err }
func (f *fakeStore) Delete(key string) error        { f.gotKey = key; return nil }

func TestBuildSSH(t *testing.T) {
	cmd := BuildSSH("alpha")
	if got := cmd.Args; !reflect.DeepEqual(got, []string{"ssh", "alpha"}) {
		t.Fatalf("args = %#v", got)
	}
}

func TestBuildSSHFS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cmd, err := BuildSSHFS(Target{
		Alias:        "alpha",
		HostName:     "example.com",
		User:         "alice",
		Port:         2222,
		IdentityFile: "~/.ssh/id_alpha",
		ProxyJump:    "jump.example",
	}, "/", filepath.Join(home, "mnt"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"sshfs", "alice@example.com:/", filepath.Join(home, "mnt"),
		"-p", "2222",
		"-o", "IdentityFile=" + filepath.Join(home, ".ssh", "id_alpha"),
		"-o", "ProxyJump=jump.example",
		"-o", "reconnect",
		"-o", "ServerAliveInterval=15",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args:\n got %#v\nwant %#v", cmd.Args, want)
	}
}

func TestBuildSSHFSVariants(t *testing.T) {
	cmd, err := BuildSSHFS(Target{HostName: "2001:db8::1", Port: 0}, "~", "/mnt/x")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sshfs", "[2001:db8::1]:~", "/mnt/x", "-p", "22", "-o", "reconnect", "-o", "ServerAliveInterval=15"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %#v", cmd.Args)
	}
	if _, err := BuildSSHFS(Target{}, "/", ""); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := BuildSSHFS(Target{HostName: "-bad"}, "/", "/mnt/x"); err == nil {
		t.Fatal("remote option injection should fail")
	}
}

func TestAskPassEnvironmentHasNoPassword(t *testing.T) {
	base := []string{"HOME=/tmp", "SSH_ASKPASS=/old", AskPassEnv + "=1", "SSH_ENHANCED_ASKPASS_KEY=old"}
	env, err := WithAskPass(base, "/usr/local/bin/betterSSH", "alice@example.com:22")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "hunter2") {
		t.Fatal("password leaked")
	}
	if !strings.Contains(joined, "SSH_ASKPASS=/usr/local/bin/betterSSH") {
		t.Fatalf("askpass executable missing: %v", env)
	}
	if !strings.Contains(joined, "SSH_ASKPASS_REQUIRE=force") {
		t.Fatal("force requirement missing")
	}
	if !strings.Contains(joined, AskPassKeyEnv+"=alice@example.com:22") {
		t.Fatal("key missing")
	}
	if strings.Count(joined, "SSH_ASKPASS=") != 1 || strings.Contains(joined, "SSH_ASKPASS=/old") {
		t.Fatalf("old askpass was not replaced: %v", env)
	}
}

func TestRunAskPass(t *testing.T) {
	store := &fakeStore{password: "s3cret"}
	env := map[string]string{AskPassEnv: "1", AskPassKeyEnv: "alice@example.com:22"}
	getenv := func(k string) string { return env[k] }
	var out bytes.Buffer
	if err := RunAskPass(getenv, store, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "s3cret\n" {
		t.Fatalf("output = %q", out.String())
	}
	if store.gotKey != "alice@example.com:22" {
		t.Fatalf("key = %q", store.gotKey)
	}

	env[AskPassKeyEnv] = ""
	if err := RunAskPass(getenv, store, io.Discard); err == nil {
		t.Fatal("missing key should fail")
	}
	delete(env, AskPassEnv)
	if err := RunAskPass(getenv, store, io.Discard); err == nil {
		t.Fatal("non-askpass mode should fail")
	}
}

func TestRunAskPassDoesNotLeakPasswordInError(t *testing.T) {
	store := &fakeStore{err: errors.New("backend unavailable")}
	env := map[string]string{AskPassEnv: "1", AskPassKeyEnv: "k"}
	err := RunAskPass(func(k string) string { return env[k] }, store, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "backend unavailable") {
		t.Fatal("store detail leaked")
	}
}

func TestParseSSHVersion(t *testing.T) {
	major, minor, ok := ParseSSHVersion("OpenSSH_9.6p1, OpenSSL 3.0.13")
	if !ok || major != 9 || minor != 6 {
		t.Fatalf("version = %d.%d ok=%v", major, minor, ok)
	}
	if _, _, ok := ParseSSHVersion("dropbear 2022"); ok {
		t.Fatal("non-OpenSSH parsed")
	}
}

func TestAskPassSupport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\necho 'OpenSSH_8.2p1, test' >&2\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	version, supported, err := AskPassSupport(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if supported || !strings.Contains(version, "8.2") {
		t.Fatalf("supported=%v version=%q", supported, version)
	}
}

var _ keyring.Store = (*fakeStore)(nil)
