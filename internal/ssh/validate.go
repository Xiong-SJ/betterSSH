package ssh

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// CheckConfig asks OpenSSH to parse configPath and resolve alias with
// "ssh -G". It never opens a network connection. On failure the returned error
// contains the first OpenSSH diagnostic line so the TUI/CLI can show it.
func CheckConfig(ctx context.Context, sshPath, configPath, alias string) error {
	if strings.TrimSpace(configPath) == "" {
		return errors.New("check config: empty path")
	}
	if strings.TrimSpace(sshPath) == "" {
		sshPath = "ssh"
	}
	if strings.TrimSpace(alias) == "" {
		alias = "betterSSH-check.invalid"
	}
	if strings.HasPrefix(alias, "-") {
		return errors.New("check config: alias must not start with '-'")
	}
	cmd := exec.CommandContext(ctx, sshPath, "-G", "-F", configPath, alias)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := firstDiagnostic(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("config check failed: %s", msg)
}

func firstDiagnostic(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Pseudo-terminal") {
			continue
		}
		return line
	}
	return ""
}
