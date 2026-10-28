//go:build linux

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

func fuseIssue() string {
	if info, err := os.Stat("/dev/fuse"); err != nil || info.IsDir() {
		return "未检测到 /dev/fuse；请安装并加载 FUSE（例如 Debian/Ubuntu: sudo apt install fuse3）"
	}
	if _, err := exec.LookPath("fusermount3"); err != nil {
		if _, err := exec.LookPath("fusermount"); err != nil {
			return "未找到 fusermount/fusermount3；请安装 fuse3 或 fuse 软件包"
		}
	}
	return ""
}

func isMounted(path string) (bool, error) {
	if mounted, err := isMountedInFile("/proc/self/mountinfo", path); err == nil && mounted {
		return true, nil
	}
	if mounted, err := isMountedInMounts("/proc/mounts", path); err == nil && mounted {
		return true, nil
	}
	// If the proc files are unavailable, fall back to passwd's mount table.
	out, err := exec.Command("mount").Output()
	if err != nil {
		return false, fmt.Errorf("check mount table: %w", err)
	}
	return parseLinuxMountOutput(string(out), path), nil
}

func isMountedInFile(file, path string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	return parseMountInfo(string(data), path), nil
}

func isMountedInMounts(file, path string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	return parseProcMounts(string(data), path), nil
}

// parseMountInfo checks the fifth field of /proc/self/mountinfo, which is the
// mount point. Paths are escaped by the kernel (\040 for spaces).
func parseMountInfo(data, path string) bool {
	path = filepath.Clean(path)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if filepath.Clean(unescapeMount(fields[4])) == path {
			return true
		}
	}
	return false
}

// parseProcMounts checks the second field of /proc/mounts.
func parseProcMounts(data, path string) bool {
	path = filepath.Clean(path)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if filepath.Clean(unescapeMount(fields[1])) == path {
			return true
		}
	}
	return false
}

func parseLinuxMountOutput(data, path string) bool {
	path = filepath.Clean(path)
	for _, line := range strings.Split(data, "\n") {
		// Example: /dev/sda1 on /mnt type ext4 (rw,relatime)
		if i := strings.Index(line, " on "); i >= 0 {
			rest := line[i+4:]
			if j := strings.Index(rest, " type "); j >= 0 {
				if filepath.Clean(rest[:j]) == path {
					return true
				}
			}
		}
	}
	return false
}

func unescapeMount(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			var v int
			ok := true
			for j := i + 1; j <= i+3; j++ {
				if s[j] < '0' || s[j] > '7' {
					ok = false
					break
				}
				v = v*8 + int(s[j]-'0')
			}
			if ok {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func unmount(ctx context.Context, path string, runner Runner) error {
	var errs []error
	candidates := [][]string{
		{"fusermount3", "-u", path},
		{"fusermount", "-u", path},
		{"umount", path},
	}
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate[0]); err != nil {
			continue
		}
		out, err := runner.Run(ctx, candidate[0], candidate[1:]...)
		if err == nil {
			return nil
		}
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			err = fmt.Errorf("%s: %w: %s", strings.Join(candidate, " "), err, msg)
		} else {
			err = fmt.Errorf("%s: %w", strings.Join(candidate, " "), err)
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return errors.New("未找到 fusermount/fusermount3/umount")
	}
	return friendlyUnmountError(path, errs)
}
