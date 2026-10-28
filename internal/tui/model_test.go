package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"betterSSH/internal/keyring"
	"betterSSH/internal/settings"
)

type fakeStore struct {
	data   map[string]string
	getErr error
	setErr error
	delErr error
}

func newFakeStore() *fakeStore { return &fakeStore{data: map[string]string{}} }

func (f *fakeStore) Set(key, password string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.data[key] = password
	return nil
}

func (f *fakeStore) Get(key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.data[key]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeStore) Delete(key string) error {
	if f.delErr != nil {
		return f.delErr
	}
	delete(f.data, key)
	return nil
}

func testConfig() string {
	return `# top
Host alpha beta # alpha comment
    # unknown stays
    HostName alpha.example
    User alice
    Port 2222
    IdentityFile ~/.ssh/id_alpha
    LocalForward 8080 localhost:80

# gap
Host gamma
    HostName gamma.example
    User bob

Host *
    User wildcard
`
}

func newTestModel(t *testing.T) (*Model, *fakeStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(testConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newFakeStore()
	m, err := New(Options{
		ConfigPath:       path,
		SettingsPath:     filepath.Join(dir, "settings.json"),
		Executable:       "/tmp/betterSSH",
		Store:            store,
		AskPassSupported: true,
		SSHVersion:       "OpenSSH_9.6p1",
	})
	if err != nil {
		t.Fatal(err)
	}
	m.width = 120
	m.height = 30
	return m, store, path
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		needle, hay string
		want        bool
	}{
		{"alpha", "alpha.example", true},
		{"aex", "alpha.example", true},
		{"zzz", "alpha.example", false},
		{"", "anything", true},
	}
	for _, c := range cases {
		if got := fuzzyMatch(c.needle, c.hay); got != c.want {
			t.Errorf("fuzzyMatch(%q,%q)=%v want %v", c.needle, c.hay, got, c.want)
		}
	}
}

func TestNewListsConcreteHostsAndFilters(t *testing.T) {
	m, _, _ := newTestModel(t)
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(m.rows))
	}
	if m.rows[0].Alias != "alpha" || m.rows[0].HostName != "alpha.example" || m.rows[0].Port != 2222 {
		t.Fatalf("first row = %+v", m.rows[0])
	}
	m.search.SetValue("gm")
	m.applyFilter()
	if len(m.visible) != 1 {
		t.Fatalf("visible = %d, want 1", len(m.visible))
	}
	if row, _ := m.selectedRow(); row.Alias != "gamma" {
		t.Fatalf("selected = %+v", row)
	}
}

func TestDeleteHostRequiresConfirmation(t *testing.T) {
	m, store, path := newTestModel(t)
	store.data["alice@alpha.example:2222"] = "stored-secret"
	m.Update(key("d"))
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want confirm", m.mode)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Fatal("backup created before confirmation")
	}
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("expected automatic keyring deletion command")
	}
	m.Update(cmd())
	if m.mode != modeList {
		t.Fatalf("mode after confirm = %v", m.mode)
	}
	if _, ok := store.data["alice@alpha.example:2222"]; ok {
		t.Fatal("keyring password was not deleted with host")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Host alpha") {
		t.Fatalf("deleted host still present:\n%s", data)
	}
	if !strings.Contains(string(data), "Host gamma") || !strings.Contains(string(data), "Host *") {
		t.Fatalf("other blocks were lost:\n%s", data)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal("backup missing")
	}
	if !strings.Contains(string(backup), "Host alpha") {
		t.Fatal("backup does not contain original")
	}
}

func TestEditHostPreservesUnknownAndComments(t *testing.T) {
	m, _, path := newTestModel(t)
	m.Update(key("e"))
	if m.mode != modeHostForm || m.form == nil {
		t.Fatalf("mode = %v form=%v", m.mode, m.form)
	}
	m.form.inputs[fieldHostName].SetValue("new.example")
	m.Update(key("enter"))
	if m.mode != modeConfirm {
		t.Fatalf("edit should require overwrite confirmation, mode=%v", m.mode)
	}
	m.Update(key("enter"))
	if m.mode != modeList {
		t.Fatalf("mode after save = %v", m.mode)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, want := range []string{"HostName new.example", "LocalForward 8080 localhost:80", "# unknown stays", "Host gamma", "# gap"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q after edit:\n%s", want, got)
		}
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatal("backup missing after edit")
	}
}

func TestSavePasswordUsesKeyringAndNeverConfig(t *testing.T) {
	m, store, path := newTestModel(t)
	m.Update(key("p"))
	if m.mode != modePassword || m.password == nil {
		t.Fatalf("mode=%v password=%v", m.mode, m.password)
	}
	m.password.input.SetValue("hunter2")
	m.Update(key("enter"))
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want confirm", m.mode)
	}
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("expected keyring command")
	}
	msg := cmd()
	m.Update(msg)
	if store.data["alice@alpha.example:2222"] != "hunter2" {
		t.Fatalf("keyring data = %#v", store.data)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "hunter2") {
		t.Fatal("password written to config")
	}
	if strings.Contains(m.status, "hunter2") {
		t.Fatal("password written to status")
	}
}

func TestSearchKeysAndClear(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.Update(key("/"))
	if m.mode != modeSearch {
		t.Fatalf("mode = %v", m.mode)
	}
	m.Update(key("g"))
	m.Update(key("a"))
	if len(m.visible) != 1 {
		t.Fatalf("visible = %d, want 1", len(m.visible))
	}
	m.Update(key("esc"))
	if m.mode != modeList || m.search.Value() != "" {
		t.Fatalf("mode=%v query=%q", m.mode, m.search.Value())
	}
}

func TestAllViewsRender(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 100, 30
	if v := m.View(); !strings.Contains(v, "betterSSH") || !strings.Contains(v, "alpha") {
		t.Fatalf("list view missing content:\n%s", v)
	}
	if v := m.View(); !strings.Contains(v, "挂载点:") || !strings.Contains(v, "~/sshfs/alpha") {
		t.Fatalf("list view does not show mount point:\n%s", v)
	}
	m.Update(key("e"))
	if v := m.View(); !strings.Contains(v, "编辑 Host") || !strings.Contains(v, "Host 别名") {
		t.Fatalf("form view missing content:\n%s", v)
	}
	m.form = nil
	m.mode = modeList
	m.Update(key("s"))
	if v := m.View(); !strings.Contains(v, "设置") || !strings.Contains(v, "严格挂载模式") {
		t.Fatalf("settings view missing content:\n%s", v)
	}
	m.settingsForm = nil
	m.mode = modeList
	m.Update(key("p"))
	if v := m.View(); !strings.Contains(v, "保存密码") {
		t.Fatalf("password view missing content:\n%s", v)
	}
	m.password = nil
	m.mode = modeList
	m.Update(key("d"))
	if v := m.View(); !strings.Contains(v, "删除 Host") {
		t.Fatalf("confirm view missing content:\n%s", v)
	}
	m.confirm = nil
	m.mode = modeHelp
	if v := m.View(); !strings.Contains(v, "安全") || !strings.Contains(v, "AskPass") {
		t.Fatalf("help view missing content:\n%s", v)
	}
	m.mode = modeMountError
	m.mountFail = &mountFailure{
		prep:    connectPrep{Alias: "alpha", MountPoint: "/tmp/mnt", RemotePath: "/"},
		message: "sshfs 失败：permission denied",
		output:  "fuse: failed",
	}
	if v := m.View(); !strings.Contains(v, "挂载失败") || !strings.Contains(v, "本机挂载点") {
		t.Fatalf("mount error view missing content:\n%s", v)
	}
	m.mountFail = nil
	m.mode = modeMountExists
	m.mountExists = &mountExisting{prep: connectPrep{Alias: "alpha", MountPoint: "/tmp/mnt", RemotePath: "/"}}
	if v := m.View(); !strings.Contains(v, "挂载点已存在") {
		t.Fatalf("existing mount view missing content:\n%s", v)
	}
}

func TestNarrowTerminalResize(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 16})
	if m.width != 40 || m.height != 16 {
		t.Fatalf("size = %dx%d", m.width, m.height)
	}
	if v := m.View(); v == "" {
		t.Fatal("empty narrow view")
	}
	m.Update(key("e"))
	if v := m.View(); v == "" {
		t.Fatal("empty narrow form view")
	}
}

func TestSettingsKeyboardEditing(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.Update(key("s"))
	if m.mode != modeSettings || m.settingsForm == nil {
		t.Fatalf("mode=%v form=%v", m.mode, m.settingsForm)
	}
	if m.settingsForm.focus != settingMountRoot {
		t.Fatalf("initial focus = %d", m.settingsForm.focus)
	}
	before := m.settingsForm.mountRoot.Value()
	m.Update(key(" "))
	m.Update(key("x"))
	if got := m.settingsForm.mountRoot.Value(); got == before || !strings.Contains(got, " x") {
		t.Fatalf("space not inserted into mount root: %q", got)
	}
	m.Update(key("tab")) // remote path
	m.Update(key("tab")) // default action
	if m.settingsForm.focus != settingDefaultAction {
		t.Fatalf("focus = %d", m.settingsForm.focus)
	}
	m.Update(key("right"))
	if m.settingsForm.draft.DefaultAction != settings.ActionMountConnect {
		t.Fatalf("default action = %q", m.settingsForm.draft.DefaultAction)
	}
	m.Update(key("tab")) // on mounted
	m.Update(key("tab")) // auto unmount
	if m.settingsForm.focus != settingAutoUnmount {
		t.Fatalf("focus = %d", m.settingsForm.focus)
	}
	m.Update(key(" "))
	if !m.settingsForm.draft.AutoUnmount {
		t.Fatal("auto unmount not toggled")
	}
}

func TestEditFormSavesPasswordAfterConfigWrite(t *testing.T) {
	m, store, path := newTestModel(t)
	m.Update(key("e"))
	m.form.savePassword = true
	m.form.inputs[fieldPassword].SetValue("new-secret")
	m.Update(key("enter")) // overwrite confirmation
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v", m.mode)
	}
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("expected password command")
	}
	msg := cmd()
	m.Update(msg)
	if store.data["alice@alpha.example:2222"] != "new-secret" {
		t.Fatalf("stored = %#v", store.data)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "new-secret") {
		t.Fatal("password written to config")
	}
	if m.form != nil {
		t.Fatal("form should be closed after successful save")
	}
}

func TestMountSetupFailureCanContinueUnlessStrict(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.Update(preparedConnectMsg{
		prep: connectPrep{WantMount: true, Alias: "alpha", HostName: "alpha.example", User: "alice", Port: 22},
		err:  errors.New("sshfs not found"),
	})
	if m.mode != modeMountError || m.mountFail == nil {
		t.Fatalf("mode=%v fail=%v", m.mode, m.mountFail)
	}
	if m.mountFail.strict {
		t.Fatal("strict mode should be off by default")
	}
	_, cmd := m.Update(key("enter"))
	if m.mode != modeList || cmd == nil {
		t.Fatalf("expected continue-to-ssh cmd, mode=%v cmd=%v", m.mode, cmd)
	}

	m.settings.StrictMount = true
	m.mode = modeList
	m.Update(preparedConnectMsg{
		prep: connectPrep{WantMount: true, Alias: "alpha", HostName: "alpha.example", User: "alice", Port: 22},
		err:  errors.New("sshfs not found"),
	})
	m.Update(key("enter"))
	if m.mode != modeMountError {
		t.Fatalf("strict mode should not continue, mode=%v", m.mode)
	}
}

func TestSSHExitPromptsAndKeepsMount(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.isMounted = func(string) (bool, error) { return true, nil }
	m.settings.AutoUnmount = false
	m.Update(sshFinishedMsg{prep: connectPrep{
		Alias: "alpha", WantMount: true, MountPoint: "~/sshfs/alpha", RemotePath: "/",
	}})
	if m.mode != modeSSHExit || m.sshExit == nil {
		t.Fatalf("expected SSH exit prompt, mode=%v prompt=%v", m.mode, m.sshExit)
	}
	if v := m.View(); !strings.Contains(v, "是否继续保留") || !strings.Contains(v, "~/sshfs/alpha") {
		t.Fatalf("prompt view missing mount point:\n%s", v)
	}
	m.Update(key("enter"))
	if m.mode != modeList || m.sshExit != nil {
		t.Fatalf("mode=%v prompt=%v", m.mode, m.sshExit)
	}
	if !strings.Contains(m.status, "已保留挂载") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestSSHExitPromptUnmount(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.isMounted = func(string) (bool, error) { return true, nil }
	m.settings.AutoUnmount = false
	m.Update(sshFinishedMsg{prep: connectPrep{
		Alias: "alpha", WantMount: true, MountPoint: "~/sshfs/alpha", RemotePath: "/",
	}})
	_, cmd := m.Update(key("u"))
	if cmd == nil {
		t.Fatal("expected unmount command")
	}
	if m.mode != modeList || m.sshExit != nil {
		t.Fatalf("mode=%v prompt=%v", m.mode, m.sshExit)
	}
	if !strings.Contains(m.busy, "卸载") {
		t.Fatalf("busy = %q", m.busy)
	}
}

func TestSSHExitAutoUnmountSkipsPrompt(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.isMounted = func(string) (bool, error) { return true, nil }
	m.settings.AutoUnmount = true
	_, cmd := m.Update(sshFinishedMsg{prep: connectPrep{
		Alias: "alpha", WantMount: true, MountPoint: "~/sshfs/alpha", RemotePath: "/",
	}})
	if m.mode == modeSSHExit || m.sshExit != nil {
		t.Fatal("auto_unmount should not prompt")
	}
	if cmd == nil {
		t.Fatal("expected auto-unmount command")
	}
}
