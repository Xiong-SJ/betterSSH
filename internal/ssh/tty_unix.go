//go:build !windows

package ssh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// promptOnTTY displays OpenSSH's confirmation prompt and reads one answer from
// the controlling terminal. AskPass itself has stdout connected to ssh, so the
// prompt must not be written there.
func promptOnTTY(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", err
	}
	defer tty.Close()

	prompt = sanitizeConfirmationPrompt(prompt)
	if prompt == "" {
		prompt = "Continue connecting? "
	}
	if !strings.HasSuffix(prompt, " ") && !strings.HasSuffix(prompt, "\n") {
		prompt += " "
	}
	if _, err := fmt.Fprint(tty, prompt); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// sanitizeConfirmationPrompt removes terminal control characters from the
// server-supplied host key prompt while preserving its line structure.
func sanitizeConfirmationPrompt(prompt string) string {
	var b strings.Builder
	b.Grow(len(prompt))
	for _, r := range prompt {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r' || r < 0x20 || r == 0x7f:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
