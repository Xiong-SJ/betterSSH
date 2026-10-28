package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"

	"betterSSH/internal/config"
	"betterSSH/internal/settings"
)

const (
	fieldAlias = iota
	fieldHostName
	fieldUser
	fieldPort
	fieldIdentity
	fieldProxyJump
	fieldAlive
	fieldPassword
	hostFieldCount
)

type hostForm struct {
	editing      bool
	index        int
	inputs       []textinput.Model
	focus        int
	savePassword bool
	err          string
	inherited    config.ResolvedHost
}

func newHostForm(in config.HostInput, editing bool, index int, inherited config.ResolvedHost, width int) *hostForm {
	placeholders := []string{
		"例如: prod-web（空格分隔可多个）", inherited.HostName, inherited.User,
		fmt.Sprintf("%d", inherited.Port), inherited.IdentityFile, inherited.ProxyJump,
		inherited.ServerAliveInterval, "输入密码后按 Ctrl+S 启用保存",
	}
	values := []string{
		strings.Join(in.Aliases, " "), in.HostName, in.User, in.Port,
		in.IdentityFile, in.ProxyJump, in.ServerAliveInterval, "",
	}
	f := &hostForm{editing: editing, index: index, inherited: inherited}
	f.inputs = make([]textinput.Model, hostFieldCount)
	for i := range f.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholders[i]
		ti.CharLimit = 512
		ti.Width = clamp(width-16, 20, 96)
		ti.SetValue(values[i])
		if i == fieldPassword {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		f.inputs[i] = ti
	}
	f.inputs[fieldAlias].Focus()
	return f
}

func (f *hostForm) setWidth(width int) {
	w := clamp(width-16, 20, 96)
	for i := range f.inputs {
		f.inputs[i].Width = w
	}
}

func (f *hostForm) setFocus(i int) {
	if i < 0 {
		i = hostFieldCount - 1
	}
	if i >= hostFieldCount {
		i = 0
	}
	f.focus = i
	for j := range f.inputs {
		if j == i {
			f.inputs[j].Focus()
		} else {
			f.inputs[j].Blur()
		}
	}
}

func (f *hostForm) toggleSavePassword() {
	f.savePassword = !f.savePassword
	if !f.savePassword {
		f.inputs[fieldPassword].SetValue("")
	}
}

func (f *hostForm) clearPassword() {
	f.inputs[fieldPassword].SetValue("")
	f.savePassword = false
}

func (f *hostForm) collect() (config.HostInput, string, error) {
	in := config.HostInput{
		Aliases:             strings.Fields(f.inputs[fieldAlias].Value()),
		HostName:            strings.TrimSpace(f.inputs[fieldHostName].Value()),
		User:                strings.TrimSpace(f.inputs[fieldUser].Value()),
		Port:                strings.TrimSpace(f.inputs[fieldPort].Value()),
		IdentityFile:        strings.TrimSpace(f.inputs[fieldIdentity].Value()),
		ProxyJump:           strings.TrimSpace(f.inputs[fieldProxyJump].Value()),
		ServerAliveInterval: strings.TrimSpace(f.inputs[fieldAlive].Value()),
	}
	if err := config.ValidateInput(&in); err != nil {
		return in, "", err
	}
	password := ""
	if f.savePassword {
		password = f.inputs[fieldPassword].Value()
		if password == "" {
			return in, "", fmt.Errorf("已启用保存密码，请输入密码")
		}
	}
	return in, password, nil
}

const (
	settingMountRoot = iota
	settingRemotePath
	settingDefaultAction
	settingOnMounted
	settingAutoUnmount
	settingStrictMount
	settingsFieldCount
)

type settingsForm struct {
	draft      settings.Settings
	mountRoot  textinput.Model
	remotePath textinput.Model
	focus      int
	err        string
}

func newSettingsForm(st settings.Settings, width int) *settingsForm {
	f := &settingsForm{draft: st}
	f.mountRoot = textinput.New()
	f.mountRoot.Prompt = ""
	f.mountRoot.SetValue(st.MountRoot)
	f.mountRoot.CharLimit = 512
	f.mountRoot.Width = clamp(width-22, 20, 80)
	f.remotePath = textinput.New()
	f.remotePath.Prompt = ""
	f.remotePath.SetValue(st.RemotePath)
	f.remotePath.CharLimit = 512
	f.remotePath.Width = clamp(width-22, 20, 80)
	f.setFocus(0)
	return f
}

func (f *settingsForm) setWidth(width int) {
	w := clamp(width-22, 20, 80)
	f.mountRoot.Width = w
	f.remotePath.Width = w
}

func (f *settingsForm) setFocus(i int) {
	if i < 0 {
		i = settingsFieldCount - 1
	}
	if i >= settingsFieldCount {
		i = 0
	}
	f.focus = i
	if i == settingMountRoot {
		f.mountRoot.Focus()
	} else {
		f.mountRoot.Blur()
	}
	if i == settingRemotePath {
		f.remotePath.Focus()
	} else {
		f.remotePath.Blur()
	}
}

func (f *settingsForm) cycle(delta int) {
	switch f.focus {
	case settingDefaultAction:
		if f.draft.DefaultAction == settings.ActionConnect {
			f.draft.DefaultAction = settings.ActionMountConnect
		} else {
			f.draft.DefaultAction = settings.ActionConnect
		}
	case settingOnMounted:
		values := []string{settings.OnMountedAsk, settings.OnMountedUnmount, settings.OnMountedUse}
		idx := 0
		for i, v := range values {
			if v == f.draft.OnMounted {
				idx = i
			}
		}
		idx = (idx + delta + len(values)) % len(values)
		f.draft.OnMounted = values[idx]
	}
}

func (f *settingsForm) toggle() {
	switch f.focus {
	case settingAutoUnmount:
		f.draft.AutoUnmount = !f.draft.AutoUnmount
	case settingStrictMount:
		f.draft.StrictMount = !f.draft.StrictMount
	}
}

func (f *settingsForm) collect() (settings.Settings, error) {
	f.draft.MountRoot = strings.TrimSpace(f.mountRoot.Value())
	f.draft.RemotePath = strings.TrimSpace(f.remotePath.Value())
	if err := f.draft.Validate(); err != nil {
		return f.draft, err
	}
	return f.draft, nil
}

type passwordForm struct {
	input textinput.Model
	key   string
	alias string
	err   string
}

func newPasswordForm(alias, key string, width int) *passwordForm {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "输入 SSH 登录密码"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 512
	ti.Width = clamp(width-12, 20, 72)
	ti.Focus()
	return &passwordForm{input: ti, key: key, alias: alias}
}

func (p *passwordForm) clear() { p.input.SetValue("") }

type confirmKind int

const (
	confirmDeleteHost confirmKind = iota
	confirmSaveHost
	confirmUnmount
	confirmSavePassword
	confirmDeletePassword
	confirmSaveSettings
)

type confirmDialog struct {
	kind       confirmKind
	title      string
	body       string
	hostIndex  int
	editing    bool
	input      config.HostInput
	password   string
	key        string
	mountPoint string
}

func (c *confirmDialog) clearSecret() {
	c.password = ""
}

type errorDialog struct {
	Title   string
	Message string
	Hint    string
}

type mountFailure struct {
	prep    connectPrep
	message string
	output  string
	strict  bool
}

type mountExisting struct {
	prep connectPrep
}

type sshExitPrompt struct {
	prep connectPrep
}
