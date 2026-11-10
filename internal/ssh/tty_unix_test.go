//go:build !windows

package ssh

import "testing"

func TestSanitizeConfirmationPrompt(t *testing.T) {
	input := "host\r\nfingerprint\x1b[31m\tred\x00\x07"
	want := "host\nfingerprint[31m\tred"
	if got := sanitizeConfirmationPrompt(input); got != want {
		t.Fatalf("sanitizeConfirmationPrompt = %q, want %q", got, want)
	}
}
