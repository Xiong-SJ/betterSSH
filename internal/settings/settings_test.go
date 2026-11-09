package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.MountRoot != "~/sshfs" || s.RemotePath != "/" || s.DefaultAction != ActionConnect {
		t.Fatalf("defaults = %+v", s)
	}
	if s.Term != DefaultTerm {
		t.Fatalf("default term = %q", s.Term)
	}
	s.Term = "screen-256color"
	s.DefaultAction = ActionMountConnect
	s.StrictMount = true
	s.MountPoints = map[string]string{"alpha": "~/mnt/alpha"}
	s.RemotePaths = map[string]string{"alpha": "~"}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultAction != ActionMountConnect || !got.StrictMount {
		t.Fatalf("loaded = %+v", got)
	}
	if got.Term != "screen-256color" {
		t.Fatalf("loaded term = %q", got.Term)
	}
	if got.MountPoint("alpha") != "~/mnt/alpha" {
		t.Fatalf("mount point = %q", got.MountPoint("alpha"))
	}
	if got.RemotePathFor("alpha") != "~" {
		t.Fatalf("remote path = %q", got.RemotePathFor("alpha"))
	}
}

func TestMountPointAndSanitize(t *testing.T) {
	s := Default()
	if got := s.MountPoint("web"); !strings.HasSuffix(got, "/sshfs/web") {
		t.Fatalf("mount point = %q", got)
	}
	s.MountRoot = "/mnt/{alias}"
	if got := s.MountPoint("a/b"); got != "/mnt/a_b" {
		t.Fatalf("sanitized mount point = %q", got)
	}
	if got := s.MountPoint(".."); !strings.HasSuffix(got, "/host") {
		t.Fatalf("dot alias = %q", got)
	}
}

func TestValidate(t *testing.T) {
	s := Default()
	s.DefaultAction = "bad"
	if err := s.Validate(); err == nil {
		t.Fatal("expected invalid action error")
	}
	s = Default()
	s.OnMounted = "bad"
	if err := s.Validate(); err == nil {
		t.Fatal("expected invalid on_mounted error")
	}
	s = Default()
	s.Term = ""
	if err := s.Validate(); err == nil {
		t.Fatal("expected empty term error")
	}
	s = Default()
	s.Term = "xterm 256color"
	if err := s.Validate(); err == nil {
		t.Fatal("expected invalid term error")
	}
}

func TestEmptySettingsUsesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.MountRoot != "~/sshfs" || s.DefaultAction != ActionConnect || s.Term != DefaultTerm {
		t.Fatalf("settings = %+v", s)
	}
}

func TestTermOrDefault(t *testing.T) {
	s := Default()
	if got := s.TermOrDefault(); got != DefaultTerm {
		t.Fatalf("default = %q", got)
	}
	s.Term = " tmux-256color "
	if got := s.TermOrDefault(); got != "tmux-256color" {
		t.Fatalf("configured = %q", got)
	}
	s.Term = ""
	if got := s.TermOrDefault(); got != DefaultTerm {
		t.Fatalf("fallback = %q", got)
	}
}

func TestLoadLegacySettingsWithoutTerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{"version":1,"mount_root":"~/sshfs","remote_path":"/","default_action":"connect","on_mounted":"ask"}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Term != DefaultTerm {
		t.Fatalf("legacy term = %q", s.Term)
	}
}
