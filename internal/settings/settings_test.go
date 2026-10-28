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
	if s.MountRoot != "~/sshfs" || s.DefaultAction != ActionConnect {
		t.Fatalf("settings = %+v", s)
	}
}
