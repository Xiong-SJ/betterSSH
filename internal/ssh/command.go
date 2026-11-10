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
	// AskPassPromptEnv is set by OpenSSH to describe the kind of prompt
	// ("confirm", "none", or unset). It is advisory; older releases do not
	// set it for host-key confirmation.
	AskPassPromptEnv = "SSH_ASKPASS_PROMPT"

	// MinAskPassMajor and MinAskPassMinor are the first OpenSSH release with
	// SSH_ASKPASS_REQUIRE=force.
	MinAskPassMajor = 8
	MinAskPassMinor = 4

	// DefaultTerm is exported to the remote side. xterm-256color enables
	// 256-color and cursor-key sequences in common terminals.
	DefaultTerm = "xterm-256color"
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
		AskPassPromptEnv:      true,
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

// WithTerm returns base with TERM set to term. Existing TERM entries (including
// duplicates) are removed first; an empty term leaves base unchanged.
func WithTerm(base []string, term string) []string {
	if term == "" {
		return base
	}
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		name := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			name = entry[:i]
		}
		if name == "TERM" {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "TERM="+term)
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

// askOnTTY is replaceable in tests. It displays OpenSSH's confirmation prompt
// on the controlling terminal and returns the user's answer.
var askOnTTY = promptOnTTY

// RunAskPass writes the saved password for the key named by the environment,
// or lets the user answer a host-key confirmation on the controlling terminal.
//
// OpenSSH routes host-key "yes/no" questions through AskPass when
// SSH_ASKPASS_REQUIRE=force is set. Returning a saved password for those
// questions makes ssh retry forever, so confirmation prompts are shown to the
// user on /dev/tty instead. Ordinary password prompts still come only from the
// keyring; the prompt text is never used to choose a saved password.
func RunAskPass(getenv func(string) string, store keyring.Store, prompt string, out io.Writer) error {
	if !IsAskPassMode(getenv) {
		return errors.New("not running in askpass mode")
	}
	if isConfirmationPrompt(prompt, getenv(AskPassPromptEnv)) {
		answer, err := askOnTTY(prompt)
		if err != nil {
			return errors.New("askpass: terminal confirmation unavailable")
		}
		if _, err := fmt.Fprintln(out, answer); err != nil {
			return fmt.Errorf("askpass: write confirmation: %w", err)
		}
		return nil
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

// isConfirmationPrompt reports whether OpenSSH is asking the user to confirm a
// host key (or key use) rather than requesting a secret. Host-key confirmation
// prompts are not tagged with SSH_ASKPASS_PROMPT on all OpenSSH versions, so
// match the stable text OpenSSH itself generates as a fallback.
func isConfirmationPrompt(prompt, hint string) bool {
	if strings.EqualFold(strings.TrimSpace(hint), "confirm") {
		return true
	}
	lower := strings.ToLower(prompt)
	return strings.Contains(lower, "the authenticity of host") ||
		strings.Contains(lower, "remote host identification has changed") ||
		strings.Contains(lower, "are you sure you want to continue connecting") ||
		strings.Contains(lower, "please type 'yes'") ||
		strings.Contains(lower, `please type "yes"`) ||
		strings.Contains(lower, "yes/no")
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
