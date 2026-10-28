//go:build darwin

package mount

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func fuseIssue() string {
	paths := []string{
		"/Library/Filesystems/macfuse.fs",
		"/Library/Filesystems/osxfuse.fs",
	}
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return ""
		}
	}
	return "未检测到 macFUSE；请安装 macFUSE（https://macfuse.github.io/）和 sshfs"
}

func isMounted(path string) (bool, error) {
	out, err := exec.Command("mount").Output()
	if err != nil {
		return false, fmt.Errorf("check mount table: %w", err)
	}
	return parseDarwinMountOutput(string(out), path), nil
}

// parseDarwinMountOutput parses lines such as:
// map auto_home on /System/Volumes/Data/home (autofs, automounted, nobrowse)
func parseDarwinMountOutput(data, path string) bool {
	path = filepath.Clean(path)
	for _, line := range strings.Split(data, "\n") {
		if i := strings.Index(line, " on "); i >= 0 {
			rest := line[i+4:]
			if j := strings.LastIndex(rest, " ("); j >= 0 {
				if filepath.Clean(rest[:j]) == path {
					return true
				}
			}
		}
	}
	return false
}

func unmount(ctx context.Context, path string, runner Runner) error {
	out, err := runner.Run(ctx, "umount", path)
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg != "" {
		err = fmt.Errorf("umount %s: %w: %s", path, err, msg)
	} else {
		err = fmt.Errorf("umount %s: %w", path, err)
	}
	return friendlyUnmountError(path, []error{err})
}
