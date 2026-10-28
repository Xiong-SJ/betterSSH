// Package ssh builds native OpenSSH and sshfs invocations. It does not
// implement an SSH client and never accepts a password on the command line.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"betterSSH/internal/keyring"
)

const (
	// AskPassEnv enables betterSSH's hidden AskPass mode.
	AskPassEnv = "SSH_ENHANCED_ASKPASS"
	// AskPassKeyEnv carries only a keyring key, never a password.
	AskPassKeyEnv = "SSH_ENHANCED_ASKPASS_KEY"
	// AskPassEnabled is the value written to AskPassEnv.
	AskPassEnabled = "1"

	// MinAskPassMajor and MinAskPassMinor are the first OpenSSH release with
	// SSH_ASKPASS_REQUIRE=force.
	MinAskPassMajor = 8
	MinAskPassMinor = 4
)

// Target is a resolved SSH host used to construct sshfs arguments.
type Target struct {
	Alias        string
	HostName     string
	User         string
	Port         int
	IdentityFile string
	ProxyJump    string
}

// BuildSSH returns the native ssh command for an alias. The config file remains
// the source of truth for all connection options.
func BuildSSH(alias string) *exec.Cmd {
	return exec.Command("ssh", alias)
}

// BuildSSHFS constructs an sshfs command. remotePath defaults to "/" and
// mountPoint must already exist.
func BuildSSHFS(target Target, remotePath, mountPoint string) (*exec.Cmd, error) {
	if strings.TrimSpace(target.HostName) == "" {
		return nil, errors.New("sshfs: HostName is empty")
	}
	if strings.TrimSpace(mountPoint) == "" {
		return nil, errors.New("sshfs: mount point is empty")
	}
	if remotePath == "" {
		remotePath = "/"
	}
	port := target.Port
	if port <= 0 {
		port = 22
	}
	host := target.HostName
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	remote := host + ":" + remotePath
	if target.User != "" {
		remote = target.User + "@" + remote
	}
	if strings.HasPrefix(remote, "-") {
		return nil, errors.New("sshfs: remote target must not start with '-'")
	}
	args := []string{remote, mountPoint, "-p", strconv.Itoa(port)}
	if target.IdentityFile != "" {
		identity, err := ExpandHome(target.IdentityFile)
		if err != nil {
			return nil, fmt.Errorf("expand IdentityFile: %w", err)
		}
		args = append(args, "-o", "IdentityFile="+identity)
	}
	if target.ProxyJump != "" {
		args = append(args, "-o", "ProxyJump="+target.ProxyJump)
	}
	args = append(args, "-o", "reconnect", "-o", "ServerAliveInterval=15")
	return exec.Command("sshfs", args...), nil
}

// ExpandHome expands a leading ~/ using the current user's home directory.
func ExpandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// WithoutAskPass removes both betterSSH and OpenSSH askpass variables so a
// fallback starts with normal terminal prompting.
func WithoutAskPass(base []string) []string {
	remove := map[string]bool{
		AskPassEnv:            true,
		AskPassKeyEnv:         true,
		"SSH_ASKPASS":         true,
		"SSH_ASKPASS_REQUIRE": true,
	}
	out := make([]string, 0, len(base))
	for _, entry := range base {
		name := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			name = entry[:i]
		}
		if remove[name] {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// WithAskPass returns an environment that delegates password prompts to the
// current executable. Only the keyring key is placed in the environment.
func WithAskPass(base []string, executable, key string) ([]string, error) {
	if key == "" {
		return nil, errors.New("askpass: empty keyring key")
	}
	exe, err := filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("askpass: resolve executable: %w", err)
	}
	out := WithoutAskPass(base)
	out = append(out,
		"SSH_ASKPASS="+exe,
		"SSH_ASKPASS_REQUIRE=force",
		AskPassEnv+"="+AskPassEnabled,
		AskPassKeyEnv+"="+key,
	)
	return out, nil
}

// ApplyAskPass sets cmd.Env using the current process environment.
func ApplyAskPass(cmd *exec.Cmd, executable, key string) error {
	env, err := WithAskPass(os.Environ(), executable, key)
	if err != nil {
		return err
	}
	cmd.Env = env
	return nil
}

// IsAskPassMode reports whether the process should act as an askpass helper.
func IsAskPassMode(getenv func(string) string) bool {
	return getenv(AskPassEnv) == AskPassEnabled
}

// RunAskPass writes the saved password for the key named by the environment.
// Prompt text supplied by OpenSSH is intentionally ignored. The password is
// only written to stdout, never to stderr or logs.
func RunAskPass(getenv func(string) string, store keyring.Store, out io.Writer) error {
	if !IsAskPassMode(getenv) {
		return errors.New("not running in askpass mode")
	}
	key := getenv(AskPassKeyEnv)
	if strings.TrimSpace(key) == "" {
		return errors.New("askpass: missing keyring key")
	}
	password, err := store.Get(key)
	if err != nil {
		return errors.New("askpass: saved password unavailable")
	}
	if strings.ContainsAny(password, "\r\n") {
		return errors.New("askpass: saved password contains a newline")
	}
	if _, err := fmt.Fprintln(out, password); err != nil {
		return fmt.Errorf("askpass: write password: %w", err)
	}
	return nil
}

// ParseSSHVersion extracts the OpenSSH version from "ssh -V" output.
func ParseSSHVersion(output string) (major, minor int, ok bool) {
	re := regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`)
	m := re.FindStringSubmatch(output)
	if len(m) != 3 {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

// AskPassSupport runs ssh -V and reports whether it supports
// SSH_ASKPASS_REQUIRE=force.
func AskPassSupport(ctx context.Context, sshPath string) (version string, supported bool, err error) {
	if sshPath == "" {
		sshPath = "ssh"
	}
	out, err := exec.CommandContext(ctx, sshPath, "-V").CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), false, err
	}
	version = strings.TrimSpace(string(out))
	major, minor, ok := ParseSSHVersion(version)
	if !ok {
		return version, false, nil
	}
	supported = major > MinAskPassMajor || (major == MinAskPassMajor && minor >= MinAskPassMinor)
	return version, supported, nil
}

// FindTool looks up a required executable.
func FindTool(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH", name)
	}
	return path, nil
}
