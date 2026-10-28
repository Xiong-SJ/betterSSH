package tui

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSanitizeBannerText(t *testing.T) {
	got := sanitizeBannerText("mount /tmp/x\nnext\x1b[31m")
	if strings.ContainsAny(got, "\n\r\x1b") {
		t.Fatalf("control characters survived: %q", got)
	}
	if !strings.Contains(got, "mount /tmp/x next") {
		t.Fatalf("banner changed unexpectedly: %q", got)
	}
}

func TestBannerExecCommandWritesBanner(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestBannerHelperProcess")
	cmd.Env = append(os.Environ(), "BETTERSSH_BANNER_HELPER=1")
	var out bytes.Buffer
	b := &bannerExecCommand{cmd: cmd, banner: "正在挂载 /tmp/remote -> /tmp/local", bannerWriter: &out}
	if err := b.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/tmp/remote -> /tmp/local") {
		t.Fatalf("banner not written: %q", out.String())
	}
}

func TestBannerHelperProcess(t *testing.T) {
	if os.Getenv("BETTERSSH_BANNER_HELPER") == "1" {
		os.Exit(0)
	}
}
