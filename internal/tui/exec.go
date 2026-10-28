package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// bannerExecCommand prints a short, password-free context line after Bubble
// Tea releases the terminal and immediately before an interactive child
// process starts. This makes the sshfs target visible while mounting and while
// the native ssh session starts.
type bannerExecCommand struct {
	cmd          *exec.Cmd
	banner       string
	bannerWriter io.Writer
}

func (b *bannerExecCommand) Run() error {
	if b.banner != "" {
		w := b.bannerWriter
		if w == nil {
			w = os.Stderr
		}
		_, _ = fmt.Fprintln(w, sanitizeBannerText(b.banner))
	}
	return b.cmd.Run()
}

func (b *bannerExecCommand) SetStdin(r io.Reader) {
	if b.cmd.Stdin == nil {
		b.cmd.Stdin = r
	}
}

func (b *bannerExecCommand) SetStdout(w io.Writer) {
	if b.cmd.Stdout == nil {
		b.cmd.Stdout = w
	}
}

func (b *bannerExecCommand) SetStderr(w io.Writer) {
	if b.cmd.Stderr == nil {
		b.cmd.Stderr = w
	}
}

// sanitizeBannerText strips control characters that could corrupt the terminal
// line. Mount paths are local settings, but they can contain newlines on Unix.
func sanitizeBannerText(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "\x1b", "").Replace(s)
	return truncate(s, 300)
}
