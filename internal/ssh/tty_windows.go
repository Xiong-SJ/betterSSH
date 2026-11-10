//go:build windows

package ssh

import "errors"

// promptOnTTY reports that no controlling terminal is available. OpenSSH on
// Windows is outside betterSSH's primary target, but the AskPass helper must
// still fail closed instead of guessing an answer.
func promptOnTTY(string) (string, error) {
	return "", errors.New("askpass: no controlling terminal")
}
