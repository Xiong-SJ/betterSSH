// Package mount handles sshfs mount points and platform-specific unmounting.
// It deliberately does not manage SSH authentication.
package mount

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner executes external commands and is injectable for tests.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands with the current environment.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// DependencyReport describes missing runtime tools.
type DependencyReport struct {
	SSH      string
	SSHFS    string
	Problems []string
}

// OK reports whether no problems were found.
func (r DependencyReport) OK() bool { return len(r.Problems) == 0 }

// CheckDependencies checks ssh, sshfs and the platform FUSE layer. Missing
// sshfs is a warning for users who only want plain connections; the TUI shows
// all problems in its footer/settings view.
func CheckDependencies() DependencyReport {
	var r DependencyReport
	if p, err := exec.LookPath("ssh"); err == nil {
		r.SSH = p
	} else {
		r.Problems = append(r.Problems, "ssh 未安装或不在 PATH 中（OpenSSH 客户端是必需依赖）")
	}
	if p, err := exec.LookPath("sshfs"); err == nil {
		r.SSHFS = p
	} else {
		r.Problems = append(r.Problems, "sshfs 未安装或不在 PATH 中（挂载功能不可用；Linux 安装 sshfs，macOS 安装 macFUSE + sshfs）")
	}
	if issue := fuseIssue(); issue != "" {
		r.Problems = append(r.Problems, issue)
	}
	return r
}

// ExpandPath expands a leading ~ and returns an absolute, cleaned path.
func ExpandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("empty path")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// ValidateMountPoint rejects dangerous targets such as "/" or the user's home
// directory (or an ancestor of it), all of which would hide important data.
func ValidateMountPoint(path string) (string, error) {
	abs, err := ExpandPath(path)
	if err != nil {
		return "", err
	}
	if abs == string(filepath.Separator) {
		return "", errors.New("refusing to use / as a mount point")
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		home = filepath.Clean(home)
		if abs == home || strings.HasPrefix(home, abs+string(filepath.Separator)) {
			return "", fmt.Errorf("refusing to mount over home directory or its parent: %s", abs)
		}
	}
	return abs, nil
}

// EnsureMountPoint expands and creates a mount directory if needed.
func EnsureMountPoint(path string) (string, error) {
	abs, err := ValidateMountPoint(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	switch {
	case err == nil && !info.IsDir():
		return "", fmt.Errorf("mount point exists but is not a directory: %s", abs)
	case err == nil:
		return abs, nil
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return "", fmt.Errorf("create mount point %s: %w", abs, err)
		}
		return abs, nil
	default:
		return "", fmt.Errorf("stat mount point %s: %w", abs, err)
	}
}

// IsMounted reports whether path is currently a mount point.
func IsMounted(path string) (bool, error) {
	abs, err := ExpandPath(path)
	if err != nil {
		return false, err
	}
	return isMounted(abs)
}

// Unmount unmounts path using the platform's normal tools. It uses fusermount
// on Linux when available and falls back to umount; macOS uses umount.
func Unmount(ctx context.Context, path string) error {
	return UnmountWith(ctx, path, ExecRunner{})
}

// UnmountWith is Unmount with an injectable Runner.
func UnmountWith(ctx context.Context, path string, runner Runner) error {
	if runner == nil {
		runner = ExecRunner{}
	}
	abs, err := ValidateMountPoint(path)
	if err != nil {
		return err
	}
	return unmount(ctx, abs, runner)
}

func friendlyUnmountError(path string, errs []error) error {
	if len(errs) == 0 {
		return fmt.Errorf("unmount %s failed", path)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "卸载 %s 失败", path)
	for _, err := range errs {
		msg := strings.TrimSpace(err.Error())
		if msg == "" {
			continue
		}
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		b.WriteString("；")
		b.WriteString(msg)
	}
	return errors.New(b.String())
}
