package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"betterSSH/internal/keyring"
)

func TestRunVersionAndHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("version exit = %d, stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "betterSSH") {
		t.Fatalf("version output = %q", out.String())
	}
	out.Reset()
	if code := run([]string{"help"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "SSH_ENHANCED_ASKPASS") || !strings.Contains(out.String(), "sshpass") {
		t.Fatalf("help output missing security docs")
	}
	if !strings.Contains(out.String(), "betterSSH connect") || !strings.Contains(out.String(), "betterSSH mount") {
		t.Fatalf("help output missing CLI examples")
	}
}

func TestRunList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "Host alpha\n    HostName alpha.example\n    User alice\n    Port 2222\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"list"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("list exit = %d, stderr=%s", code, errOut.String())
	}
	for _, want := range []string{"alpha", "alpha.example", "alice", "2222"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"definitely-not-a-command"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

type fakePasswordStore struct {
	password string
	err      error
}

func (f fakePasswordStore) Set(key, password string) error { return nil }
func (f fakePasswordStore) Get(key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.password, nil
}
func (f fakePasswordStore) Delete(key string) error { return nil }

func TestAskPassDispatch(t *testing.T) {
	old := passwordStore
	passwordStore = fakePasswordStore{password: "correct horse battery staple"}
	defer func() { passwordStore = old }()

	t.Setenv("SSH_ENHANCED_ASKPASS", "1")
	t.Setenv("SSH_ENHANCED_ASKPASS_KEY", "alice@example.com:22")
	var out, errOut bytes.Buffer
	if code := run([]string{"askpass", "alice@example.com's password:"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("askpass exit = %d, stderr=%q", code, errOut.String())
	}
	if out.String() != "correct horse battery staple\n" {
		t.Fatalf("askpass stdout = %q", out.String())
	}
}

func TestAskPassFailureIsGeneric(t *testing.T) {
	old := passwordStore
	passwordStore = fakePasswordStore{err: os.ErrPermission}
	defer func() { passwordStore = old }()

	t.Setenv("SSH_ENHANCED_ASKPASS", "1")
	t.Setenv("SSH_ENHANCED_ASKPASS_KEY", "alice@example.com:22")
	var out, errOut bytes.Buffer
	if code := run([]string{"askpass"}, strings.NewReader(""), &out, &errOut); code != 1 {
		t.Fatalf("askpass exit = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Fatalf("failed askpass leaked stdout: %q", out.String())
	}
	if strings.Contains(errOut.String(), "permission") {
		t.Fatalf("askpass error leaked keyring detail: %q", errOut.String())
	}
}

func setupCLIHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	oldStore := passwordStore
	passwordStore = &mapPasswordStore{data: map[string]string{}}
	t.Cleanup(func() { passwordStore = oldStore })
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `# keep
Host alpha
    HostName alpha.example
    User alice
    Port 2222
    LocalForward 8080 localhost:80
`
	path := filepath.Join(home, ".ssh", "config")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCLI(input string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(input), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCLIAddShowEditRemove(t *testing.T) {
	path := setupCLIHome(t)

	code, out, errOut := runCLI("", "add", "beta", "--hostname", "beta.example", "--user", "bob", "--port", "2200")
	if code != 0 {
		t.Fatalf("add exit=%d out=%s err=%s", code, out, errOut)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Host beta") || !strings.Contains(string(data), "HostName beta.example") {
		t.Fatalf("added host missing:\n%s", data)
	}

	code, out, errOut = runCLI("", "show", "alpha")
	if code != 0 {
		t.Fatalf("show exit=%d err=%s", code, errOut)
	}
	for _, want := range []string{"HostName: alpha.example", "User: alice", "Port: 2222", "MountPoint:", "KeyringKey: alice@alpha.example:2222"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}

	code, out, errOut = runCLI("", "edit", "alpha", "--user", "carol", "--yes")
	if code != 0 {
		t.Fatalf("edit exit=%d out=%s err=%s", code, out, errOut)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "User carol") || !strings.Contains(string(data), "LocalForward 8080 localhost:80") {
		t.Fatalf("edit did not preserve unknown field:\n%s", data)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatal("backup missing after edit")
	}

	code, out, _ = runCLI("n\n", "edit", "alpha", "--user", "dave")
	if code != 0 || !strings.Contains(out, "已取消") {
		t.Fatalf("declined edit code=%d out=%s", code, out)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "User carol") || strings.Contains(string(data), "User dave") {
		t.Fatalf("declined edit modified config:\n%s", data)
	}

	code, out, errOut = runCLI("", "remove", "beta", "--yes")
	if code != 0 {
		t.Fatalf("remove exit=%d out=%s err=%s", code, out, errOut)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "Host beta") {
		t.Fatalf("removed host still present:\n%s", data)
	}
}

func TestCLIUnmountNotMounted(t *testing.T) {
	setupCLIHome(t)
	code, out, errOut := runCLI("", "unmount", "alpha", "--yes")
	if code != 0 {
		t.Fatalf("unmount exit=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "当前未挂载") {
		t.Fatalf("output = %s", out)
	}
}

func TestCLIUsageAndMissingHost(t *testing.T) {
	setupCLIHome(t)
	if code, _, errOut := runCLI("", "connect", "alpha", "--mount", "--no-mount"); code != 2 || !strings.Contains(errOut, "不能同时使用") {
		t.Fatalf("conflicting flags code=%d err=%s", code, errOut)
	}
	if code, _, errOut := runCLI("", "connect", "missing"); code != 1 || !strings.Contains(errOut, "不存在") {
		t.Fatalf("missing host code=%d err=%s", code, errOut)
	}
	if code, _, errOut := runCLI("", "show", "missing"); code != 1 || !strings.Contains(errOut, "不存在") {
		t.Fatalf("missing show code=%d err=%s", code, errOut)
	}
}

type mapPasswordStore struct {
	data map[string]string
}

func (s *mapPasswordStore) Set(key, password string) error {
	s.data[key] = password
	return nil
}

func (s *mapPasswordStore) Get(key string) (string, error) {
	v, ok := s.data[key]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (s *mapPasswordStore) Delete(key string) error {
	delete(s.data, key)
	return nil
}

func TestCLIRemoveDeletesKeyringPassword(t *testing.T) {
	path := setupCLIHome(t)
	store := &mapPasswordStore{data: map[string]string{
		"alice@alpha.example:2222": "stored-secret",
	}}
	oldStore := passwordStore
	passwordStore = store
	t.Cleanup(func() { passwordStore = oldStore })

	code, out, errOut := runCLI("", "remove", "alpha", "--yes")
	if code != 0 {
		t.Fatalf("remove exit=%d out=%s err=%s", code, out, errOut)
	}
	if _, ok := store.data["alice@alpha.example:2222"]; ok {
		t.Fatal("keyring password was not deleted")
	}
	if !strings.Contains(out, "已删除系统钥匙串密码") {
		t.Fatalf("output missing keyring deletion confirmation: %s", out)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "Host alpha") {
		t.Fatalf("host was not removed:\n%s", data)
	}
}
