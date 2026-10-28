//go:build !linux && !darwin

package mount

import (
	"context"
	"errors"
)

func fuseIssue() string {
	return "当前平台不支持自动挂载状态检测；请使用 Linux 或 macOS"
}

func isMounted(path string) (bool, error) {
	return false, errors.New("mount detection is not implemented on this platform")
}

func unmount(ctx context.Context, path string, runner Runner) error {
	return errors.New("unmount is not implemented on this platform")
}
