package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"betterSSH/internal/config"
	"betterSSH/internal/keyring"
	"betterSSH/internal/mount"
	"betterSSH/internal/settings"
	sshutil "betterSSH/internal/ssh"
)

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.width < 40 {
			m.width = 40
		}
		if m.height < 16 {
			m.height = 16
		}
		m.search.Width = clamp(m.width-12, 20, 120)
		if m.form != nil {
			m.form.setWidth(m.width)
		}
		if m.settingsForm != nil {
			m.settingsForm.setWidth(m.width)
		}
		if m.password != nil {
			m.password.input.Width = clamp(m.width-12, 20, 72)
		}
		m.ensureVisible()
		return m, nil
	case preparedConnectMsg:
		return m.handlePrepared(msg)
	case mountFinishedMsg:
		return m.handleMountFinished(msg)
	case sshFinishedMsg:
		return m.handleSSHFinished(msg)
	case unmountedMsg:
		return m.handleUnmounted(msg)
	case passwordSavedMsg:
		if msg.err != nil {
			m.setStatus("保存密码失败："+msg.err.Error(), true)
		} else {
			m.setStatus("密码已保存到系统钥匙串（service="+keyring.Service+"，key="+msg.key+"）", false)
		}
		return m, nil
	case passwordDeletedMsg:
		switch {
		case msg.err != nil:
			m.setStatus("删除系统钥匙串密码失败（"+msg.key+"）："+msg.err.Error(), true)
		case !msg.found:
			m.setStatus("未发现对应已保存密码（"+msg.key+"）", false)
		default:
			m.setStatus("已删除系统钥匙串密码（"+msg.key+"）", false)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	default:
		if cmd := m.forwardInput(msg); cmd != nil {
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.busy != "" && m.mode == modeList {
		return m, nil
	}
	switch m.mode {
	case modeSearch:
		return m.handleSearchKey(msg)
	case modeHostForm:
		return m.handleHostFormKey(msg)
	case modeSettings:
		return m.handleSettingsKey(msg)
	case modePassword:
		return m.handlePasswordKey(msg)
	case modeConfirm:
		return m.handleConfirmKey(msg)
	case modeError:
		switch msg.String() {
		case "enter", "esc", " ":
			m.errDialog = nil
			m.mode = modeList
		}
		return m, nil
	case modeMountError:
		return m.handleMountErrorKey(msg)
	case modeMountExists:
		return m.handleMountExistsKey(msg)
	case modeSSHExit:
		return m.handleSSHExitKey(msg)
	case modeHelp:
		switch msg.String() {
		case "esc", "enter", "q", "?":
			m.mode = modeList
		}
		return m, nil
	default:
		return m.handleListKey(msg)
	}
}

func (m *Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "enter":
		return m.startConnect(m.settings.DefaultAction == settings.ActionMountConnect)
	case "m", "M", "ctrl+m":
		return m.startConnect(true)
	case "e":
		return m.openHostForm(false)
	case "n":
		return m.openHostForm(true)
	case "d":
		return m.confirmDeleteHost()
	case "u":
		return m.confirmUnmount()
	case "s":
		m.openSettings()
		return m, nil
	case "p":
		return m.openPasswordForm()
	case "x":
		return m.confirmDeletePassword()
	case "/":
		m.mode = modeSearch
		return m, m.search.Focus()
	case "?":
		m.mode = modeHelp
		return m, nil
	case "r":
		m.reloadAll()
		return m, nil
	case "esc":
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.applyFilter()
			m.setStatus("已清除搜索", false)
		}
		return m, nil
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "pgup":
		m.moveSelection(-m.pageSize())
	case "pgdown":
		m.moveSelection(m.pageSize())
	case "home", "g":
		m.sel = 0
		m.clampSelection()
	case "end", "G":
		if len(m.visible) > 0 {
			m.sel = len(m.visible) - 1
			m.clampSelection()
		}
	}
	return m, nil
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.search.SetValue("")
		m.search.Blur()
		m.applyFilter()
		m.mode = modeList
		return m, nil
	case "enter":
		m.search.Blur()
		m.mode = modeList
		return m, nil
	case "up":
		m.moveSelection(-1)
		return m, nil
	case "down":
		m.moveSelection(1)
		return m, nil
	default:
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.applyFilter()
		return m, cmd
	}
}

func (m *Model) handleHostFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.form
	if f == nil {
		m.mode = modeList
		return m, nil
	}
	switch msg.String() {
	case "esc":
		f.clearPassword()
		m.form = nil
		m.mode = modeList
		return m, nil
	case "tab", "down":
		f.setFocus(f.focus + 1)
		return m, nil
	case "shift+tab", "up":
		f.setFocus(f.focus - 1)
		return m, nil
	case "ctrl+s":
		f.toggleSavePassword()
		if f.savePassword {
			f.setFocus(fieldPassword)
		}
		f.err = ""
		return m, nil
	case "enter":
		in, password, err := f.collect()
		if err != nil {
			f.err = err.Error()
			return m, nil
		}
		if f.editing {
			body := fmt.Sprintf("将覆盖 Host %s 的常用字段，保留未知字段和注释，并在写入前生成 config.bak。", strings.Join(in.Aliases, " "))
			if password != "" {
				body += "\n\n密码将同时写入系统钥匙串（可能覆盖该目标已保存的密码）。"
			}
			m.confirm = &confirmDialog{
				kind:      confirmSaveHost,
				title:     "覆盖 Host 配置？",
				body:      body,
				hostIndex: f.index,
				editing:   true,
				input:     in,
				password:  password,
			}
			m.mode = modeConfirm
			return m, nil
		}
		return m, m.saveHost(false, 0, in, password)
	}
	if f.focus == fieldPassword && !f.savePassword && msg.Type == tea.KeyRunes {
		f.savePassword = true
	}
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	f.err = ""
	return m, cmd
}

func (m *Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.settingsForm
	if f == nil {
		m.mode = modeList
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.settingsForm = nil
		m.mode = modeList
		return m, nil
	case "tab", "down":
		f.setFocus(f.focus + 1)
		return m, nil
	case "shift+tab", "up":
		f.setFocus(f.focus - 1)
		return m, nil
	case "left", "right":
		if f.focus == settingDefaultAction || f.focus == settingOnMounted {
			delta := 1
			if msg.String() == "left" {
				delta = -1
			}
			f.cycle(delta)
			return m, nil
		}
	case " ":
		if f.focus == settingAutoUnmount || f.focus == settingStrictMount {
			f.toggle()
			return m, nil
		}
	case "enter":
		st, err := f.collect()
		if err != nil {
			f.err = err.Error()
			return m, nil
		}
		f.draft = st
		m.confirm = &confirmDialog{
			kind:  confirmSaveSettings,
			title: "保存设置？",
			body:  "将覆盖 " + displayPath(m.SettingsPath) + "（不修改 ~/.ssh/config）。",
		}
		m.mode = modeConfirm
		return m, nil
	}
	var cmd tea.Cmd
	switch f.focus {
	case settingMountRoot:
		f.mountRoot, cmd = f.mountRoot.Update(msg)
	case settingRemotePath:
		f.remotePath, cmd = f.remotePath.Update(msg)
	}
	f.err = ""
	return m, cmd
}

func (m *Model) handlePasswordKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.password
	if p == nil {
		m.mode = modeList
		return m, nil
	}
	switch msg.String() {
	case "esc":
		p.clear()
		m.password = nil
		m.mode = modeList
		return m, nil
	case "enter":
		password := p.input.Value()
		if password == "" {
			p.err = "密码不能为空"
			return m, nil
		}
		m.confirm = &confirmDialog{
			kind:     confirmSavePassword,
			title:    "保存密码到系统钥匙串？",
			body:     fmt.Sprintf("service=%s\nkey=%s\n\n密码只写入操作系统钥匙串，不写入配置、日志或环境变量。", keyring.Service, p.key),
			key:      p.key,
			password: password,
		}
		m.mode = modeConfirm
		return m, nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.err = ""
	return m, cmd
}

func (m *Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.confirm
	if c == nil {
		m.mode = modeList
		return m, nil
	}
	switch msg.String() {
	case "n", "N", "esc":
		c.clearSecret()
		m.confirm = nil
		if c.kind == confirmSaveHost {
			m.mode = modeHostForm
		} else if c.kind == confirmSaveSettings {
			m.mode = modeSettings
		} else {
			m.mode = modeList
		}
		return m, nil
	case "y", "Y", "enter":
		m.confirm = nil
		return m.executeConfirm(c)
	}
	return m, nil
}

func (m *Model) executeConfirm(c *confirmDialog) (tea.Model, tea.Cmd) {
	switch c.kind {
	case confirmDeleteHost:
		m.mode = modeList
		return m, m.deleteHost(c.hostIndex)
	case confirmSaveHost:
		password := c.password
		c.clearSecret()
		return m, m.saveHost(c.editing, c.hostIndex, c.input, password)
	case confirmUnmount:
		m.mode = modeList
		m.busy = "卸载中..."
		return m, m.unmountCmd(c.mountPoint, nil, false)
	case confirmSavePassword:
		m.mode = modeList
		if m.password != nil {
			m.password.clear()
			m.password = nil
		}
		password := c.password
		c.clearSecret()
		m.setStatus("正在保存密码到系统钥匙串...", false)
		return m, m.storePasswordCmd(c.key, password)
	case confirmDeletePassword:
		m.mode = modeList
		m.setStatus("正在删除已保存密码...", false)
		return m, m.deletePasswordCmd(c.key)
	case confirmSaveSettings:
		m.saveSettings()
		return m, nil
	}
	m.mode = modeList
	return m, nil
}

func (m *Model) handleMountErrorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.mountFail
	if f == nil {
		m.mode = modeList
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.mountFail = nil
		m.mode = modeList
		m.setStatus("已取消挂载；sshfs 挂载点未建立", false)
	case "enter":
		if f.strict {
			m.setStatus("严格挂载模式已开启：挂载失败时不会继续连接", true)
			return m, nil
		}
		m.mountFail = nil
		m.mode = modeList
		m.setStatus("挂载失败，按当前设置继续 SSH 连接", false)
		return m, m.launchSSH(f.prep)
	case "r":
		prep := f.prep
		prep.RemotePath = "~"
		m.mountFail = nil
		m.mode = modeList
		m.setStatus("使用远程路径 ~ 重试挂载...", false)
		return m, m.launchMount(prep)
	case "s":
		m.mountFail = nil
		m.openSettings()
	}
	return m, nil
}

func (m *Model) handleMountExistsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.mountExists
	if e == nil {
		m.mode = modeList
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.mountExists = nil
		m.mode = modeList
		m.setStatus("已取消", false)
	case "c":
		prep := e.prep
		m.mountExists = nil
		m.mode = modeList
		m.setStatus("复用已有挂载点，连接 "+prep.Alias, false)
		return m, m.launchSSH(prep)
	case "u":
		prep := e.prep
		m.mountExists = nil
		m.mode = modeList
		m.busy = "卸载已有挂载..."
		return m, m.unmountCmd(prep.MountPoint, &prep, true)
	case "s":
		m.mountExists = nil
		m.openSettings()
	}
	return m, nil
}

func (m *Model) handlePrepared(msg preparedConnectMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	if msg.err != nil {
		if msg.prep.WantMount && msg.prep.Alias != "" {
			m.showMountSetupFailure(msg.prep, msg.err)
			return m, nil
		}
		m.showError("无法准备连接", msg.err.Error(), "检查 Host 配置和挂载路径后重试。")
		return m, nil
	}
	prep := msg.prep
	if prep.AskPassWarn != "" {
		m.setStatus(prep.AskPassWarn, true)
	}
	if !prep.WantMount {
		return m, m.launchSSH(prep)
	}
	if msg.mounted {
		switch m.settings.OnMounted {
		case settings.OnMountedUse:
			m.setStatus("复用已有挂载点，连接 "+prep.Alias, false)
			return m, m.launchSSH(prep)
		case settings.OnMountedUnmount:
			m.busy = "卸载已有挂载..."
			return m, m.unmountCmd(prep.MountPoint, &prep, true)
		default:
			m.mountExists = &mountExisting{prep: prep}
			m.mode = modeMountExists
			return m, nil
		}
	}
	return m, m.launchMount(prep)
}

func (m *Model) showMountSetupFailure(prep connectPrep, err error) {
	m.mountFail = &mountFailure{
		prep:    prep,
		message: mountFailureMessage(err, "", false, m.deps),
		strict:  m.settings.StrictMount,
	}
	m.mode = modeMountError
}

func (m *Model) handleSSHExitKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.sshExit
	if e == nil {
		m.mode = modeList
		return m, nil
	}
	switch msg.String() {
	case "enter", "k", "K", "esc":
		m.sshExit = nil
		m.mode = modeList
		m.setStatus("已保留挂载 "+displayPath(e.prep.MountPoint)+"；稍后可按 u 卸载", false)
		return m, nil
	case "u", "U", "d", "D":
		m.sshExit = nil
		m.mode = modeList
		m.busy = "卸载 " + displayPath(e.prep.MountPoint) + "..."
		return m, m.unmountCmd(e.prep.MountPoint, nil, false)
	}
	return m, nil
}

func (m *Model) handleMountFinished(msg mountFinishedMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	if msg.err == nil && msg.mounted {
		m.setStatus("已挂载到 "+displayPath(msg.prep.MountPoint)+"，正在连接 "+msg.prep.Alias, false)
		return m, m.launchSSH(msg.prep)
	}
	m.mountFail = &mountFailure{
		prep:    msg.prep,
		message: mountFailureMessage(msg.err, msg.output, msg.mounted, m.deps),
		output:  sanitizeMountOutput(msg.output),
		strict:  m.settings.StrictMount,
	}
	m.mode = modeMountError
	return m, nil
}

func (m *Model) handleSSHFinished(msg sshFinishedMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	if msg.err != nil {
		var exitErr *exec.ExitError
		if errors.As(msg.err, &exitErr) {
			m.setStatus(fmt.Sprintf("SSH 会话结束（退出码 %d）", exitErr.ExitCode()), exitErr.ExitCode() != 0)
		} else {
			m.setStatus("SSH 会话结束："+msg.err.Error(), true)
		}
	} else {
		m.setStatus("SSH 会话已结束", false)
	}
	if msg.prep.WantMount && msg.prep.MountPoint != "" {
		if mounted, _ := m.checkMounted(msg.prep.MountPoint); mounted {
			if m.settings.AutoUnmount {
				m.busy = "按设置自动卸载..."
				return m, m.unmountCmd(msg.prep.MountPoint, nil, false)
			}
			m.sshExit = &sshExitPrompt{prep: msg.prep}
			m.mode = modeSSHExit
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) handleUnmounted(msg unmountedMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	if msg.err != nil {
		m.showError("卸载失败", msg.err.Error(), "关闭占用该目录的程序后重试；Linux 可检查 fusermount/umount，macOS 使用 umount。")
		return m, nil
	}
	m.setStatus("已卸载 "+displayPath(msg.path), false)
	if msg.remount && msg.prep != nil {
		prep := *msg.prep
		return m, m.launchMount(prep)
	}
	return m, nil
}

func (m *Model) startConnect(wantMount bool) (tea.Model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		m.setStatus("没有可连接的 Host", true)
		return m, nil
	}
	if row.ResolveErr != nil {
		m.showError("无法解析 Host", row.ResolveErr.Error(), "按 e 修正配置后重试。")
		return m, nil
	}
	if strings.HasPrefix(row.Alias, "-") {
		m.showError("Host 别名不安全", "别名不能以 '-' 开头，否则可能被 ssh 当作命令行选项。", "按 e 修改别名为普通主机名后重试。")
		return m, nil
	}
	if m.deps.SSH == "" {
		m.showError("缺少 ssh", "OpenSSH 客户端未安装或不在 PATH 中。", "Linux: 安装 openssh-client；macOS: 系统自带 /usr/bin/ssh。")
		return m, nil
	}
	m.busy = "准备连接 " + row.Alias + "..."
	return m, m.prepareConnectionCmd(row.Index, wantMount)
}

func (m *Model) prepareConnectionCmd(index int, wantMount bool) tea.Cmd {
	cfg := m.cfg
	st := m.settings
	store := m.Store
	exe := m.Executable
	askPassSupported := m.askPassSupported
	return func() tea.Msg {
		resolved, err := cfg.Resolve(index)
		if err != nil {
			return preparedConnectMsg{err: err}
		}
		prep := connectPrep{
			Alias:     resolved.Alias,
			HostName:  resolved.HostName,
			User:      resolved.User,
			Port:      resolved.Port,
			Identity:  resolved.IdentityFile,
			ProxyJump: resolved.ProxyJump,
			WantMount: wantMount,
		}
		mounted := false
		if wantMount {
			if _, err := exec.LookPath("sshfs"); err != nil {
				return preparedConnectMsg{prep: prep, err: fmt.Errorf("sshfs 未安装或不在 PATH 中；挂载功能不可用。Linux: 安装 sshfs；macOS: 安装 macFUSE + sshfs")}
			}
			rawPoint := st.MountPoint(resolved.Alias)
			point, err := mount.EnsureMountPoint(rawPoint)
			if err != nil {
				return preparedConnectMsg{prep: prep, err: err}
			}
			prep.MountPoint = point
			prep.RemotePath = st.RemotePathFor(resolved.Alias)
			mounted, err = mount.IsMounted(point)
			if err != nil {
				return preparedConnectMsg{prep: prep, err: fmt.Errorf("检查挂载状态: %w", err)}
			}
		}
		key := keyring.Key(resolved.User, resolved.HostName, resolved.Port)
		if _, err := store.Get(key); err == nil {
			if askPassSupported && exe != "" {
				prep.AskPass = true
				prep.AskPassKey = key
			} else if exe == "" {
				prep.AskPassWarn = "无法确定当前可执行文件路径，已回退到终端交互输入密码"
			} else {
				prep.AskPassWarn = "已保存密码，但当前 OpenSSH 不支持 SSH_ASKPASS_REQUIRE=force；已回退到终端交互输入密码"
			}
		} else if !errors.Is(err, keyring.ErrNotFound) {
			prep.AskPassWarn = "读取系统钥匙串失败，将回退到终端交互输入密码"
		}
		return preparedConnectMsg{prep: prep, mounted: mounted}
	}
}

func (m *Model) launchMount(prep connectPrep) tea.Cmd {
	if _, err := sshutil.FindTool("sshfs"); err != nil {
		m.showMountSetupFailure(prep, fmt.Errorf("无法执行挂载：%w", err))
		return nil
	}
	cmd, err := sshutil.BuildSSHFS(sshutil.Target{
		Alias:        prep.Alias,
		HostName:     prep.HostName,
		User:         prep.User,
		Port:         prep.Port,
		IdentityFile: prep.Identity,
		ProxyJump:    prep.ProxyJump,
	}, prep.RemotePath, prep.MountPoint)
	if err != nil {
		m.showMountSetupFailure(prep, fmt.Errorf("无法构造 sshfs 命令：%w", err))
		return nil
	}
	if prep.AskPass {
		if err := sshutil.ApplyAskPass(cmd, m.Executable, prep.AskPassKey); err != nil {
			cmd.Env = sshutil.WithoutAskPass(os.Environ())
			m.setStatus("AskPass 启动失败，回退到终端输入密码", true)
		}
	} else {
		cmd.Env = sshutil.WithoutAskPass(os.Environ())
	}
	var captured bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &captured)
	cmd.Stderr = io.MultiWriter(os.Stderr, &captured)
	m.busy = "挂载 " + prep.Alias + " 到 " + displayPath(prep.MountPoint) + "..."
	banner := fmt.Sprintf("betterSSH: 正在挂载 %s:%s -> %s", remoteTarget(prep), prep.RemotePath, prep.MountPoint)
	return tea.Exec(&bannerExecCommand{cmd: cmd, banner: banner}, func(err error) tea.Msg {
		mounted, _ := mount.IsMounted(prep.MountPoint)
		return mountFinishedMsg{prep: prep, output: captured.String(), mounted: mounted, err: err}
	})
}

func (m *Model) launchSSH(prep connectPrep) tea.Cmd {
	cmd := sshutil.BuildSSH(prep.Alias)
	if prep.AskPass {
		if err := sshutil.ApplyAskPass(cmd, m.Executable, prep.AskPassKey); err != nil {
			cmd.Env = sshutil.WithoutAskPass(os.Environ())
			m.setStatus("AskPass 启动失败，回退到终端输入密码", true)
		}
	} else {
		cmd.Env = sshutil.WithoutAskPass(os.Environ())
	}
	m.busy = "连接 " + prep.Alias + "..."
	banner := ""
	if prep.WantMount && prep.MountPoint != "" {
		banner = fmt.Sprintf("betterSSH: sshfs 已挂载 %s:%s -> %s；正在启动 ssh %s",
			remoteTarget(prep), prep.RemotePath, prep.MountPoint, prep.Alias)
	}
	return tea.Exec(&bannerExecCommand{cmd: cmd, banner: banner}, func(err error) tea.Msg {
		return sshFinishedMsg{prep: prep, err: err}
	})
}

func remoteTarget(prep connectPrep) string {
	target := prep.HostName
	if prep.User != "" {
		target = prep.User + "@" + target
	}
	return target
}

func (m *Model) storePasswordCmd(key, password string) tea.Cmd {
	store := m.Store
	return func() tea.Msg {
		err := store.Set(key, password)
		return passwordSavedMsg{key: key, err: err}
	}
}

func (m *Model) deletePasswordCmd(key string) tea.Cmd {
	store := m.Store
	return func() tea.Msg {
		_, err := store.Get(key)
		if errors.Is(err, keyring.ErrNotFound) {
			return passwordDeletedMsg{key: key, found: false}
		}
		if err != nil {
			return passwordDeletedMsg{key: key, found: true, err: err}
		}
		err = store.Delete(key)
		return passwordDeletedMsg{key: key, found: true, err: err}
	}
}

func (m *Model) unmountCmd(path string, prep *connectPrep, remount bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := mount.Unmount(ctx, path)
		return unmountedMsg{path: path, prep: prep, remount: remount, err: err}
	}
}

func (m *Model) openHostForm(forceNew bool) (tea.Model, tea.Cmd) {
	if forceNew {
		f := newHostForm(config.HostInput{}, false, 0, config.ResolvedHost{Port: config.DefaultPort}, m.width)
		m.form = f
		m.mode = modeHostForm
		return m, textInputBlink()
	}
	row, ok := m.selectedRow()
	if !ok {
		m.setStatus("没有可编辑的 Host", true)
		return m, nil
	}
	resolved, err := m.cfg.Resolve(row.Index)
	if err != nil {
		m.showError("无法解析 Host", err.Error(), "按 e 之前请先修复配置文件。")
		return m, nil
	}
	h := m.cfg.Hosts[row.Index]
	f := newHostForm(config.InputFromHost(h), true, row.Index, resolved, m.width)
	m.form = f
	m.mode = modeHostForm
	return m, textInputBlink()
}

func (m *Model) saveHost(editing bool, index int, in config.HostInput, password string) tea.Cmd {
	existed := false
	if _, err := os.Stat(m.ConfigPath); err == nil {
		existed = true
	}
	var err error
	if editing {
		err = m.cfg.UpdateHost(index, in)
	} else {
		err = m.cfg.AddHost(in)
	}
	if err != nil {
		if m.form != nil {
			m.form.err = err.Error()
			m.mode = modeHostForm
		} else {
			m.showError("保存 Host 失败", err.Error(), "按 e 修改后重试。")
		}
		return nil
	}
	if err := m.cfg.Save(); err != nil {
		// The in-memory block indexes are no longer trustworthy (the file may
		// have changed on disk). Drop the form and force a reload rather than
		// risking a later write against the wrong Host block.
		if m.form != nil {
			m.form.clearPassword()
		}
		m.form = nil
		if cfg, loadErr := config.Load(m.ConfigPath); loadErr == nil {
			m.cfg = cfg
			m.refreshRows("")
		}
		hint := "检查文件权限后按 r 重新加载。"
		if errors.Is(err, config.ErrModified) {
			hint = "配置文件已被其他程序修改；已拒绝覆盖。按 r 重新加载后再编辑。"
		}
		m.mode = modeList
		m.showError("保存配置失败", err.Error(), hint)
		return nil
	}
	alias := config.PrimaryAlias(in.Aliases)
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		if m.form != nil {
			m.form.clearPassword()
		}
		m.form = nil
		m.mode = modeList
		m.showError("重新加载配置失败", err.Error(), "文件已经写入，请按 r 重试。")
		return nil
	}
	m.cfg = cfg
	m.refreshRows(alias)
	if m.form != nil {
		m.form.clearPassword()
	}
	m.form = nil
	m.mode = modeList
	msg := "已保存 Host " + alias
	if existed {
		msg += "；备份 " + displayPath(m.cfg.BackupPath())
	}
	m.setStatus(msg, false)
	if password != "" {
		if rowIdx := findConfigIndex(m.cfg, in.Aliases); rowIdx >= 0 {
			if resolved, err := m.cfg.Resolve(rowIdx); err == nil {
				key := keyring.Key(resolved.User, resolved.HostName, resolved.Port)
				m.setStatus("Host 已保存，正在保存密码到系统钥匙串...", false)
				return m.storePasswordCmd(key, password)
			}
		}
		m.setStatus("Host 已保存，但无法定位密码 key，密码未保存", true)
	}
	return nil
}

func (m *Model) openSettings() {
	m.settingsForm = newSettingsForm(m.settings, m.width)
	m.mode = modeSettings
}

func (m *Model) saveSettings() {
	f := m.settingsForm
	if f == nil {
		m.mode = modeList
		return
	}
	st, err := f.collect()
	if err != nil {
		f.err = err.Error()
		m.mode = modeSettings
		return
	}
	if err := settings.Save(m.SettingsPath, st); err != nil {
		f.err = err.Error()
		m.mode = modeSettings
		return
	}
	loaded, err := settings.Load(m.SettingsPath)
	if err != nil {
		m.showError("设置已写入但重新读取失败", err.Error(), "检查 settings.json 权限后重试。")
		m.settingsForm = nil
		m.mode = modeList
		return
	}
	m.settings = loaded
	m.settingsForm = nil
	m.mode = modeList
	m.setStatus("设置已保存："+displayPath(m.SettingsPath), false)
}

func (m *Model) confirmDeleteHost() (tea.Model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	m.confirm = &confirmDialog{
		kind:      confirmDeleteHost,
		title:     "删除 Host？",
		body:      fmt.Sprintf("将从 %s 删除 Host %s 及其字段，并自动删除对应的系统钥匙串密码。\n注释、空行和其他 Host 会保留；写入前生成 config.bak。\n\n此操作不可撤销。", displayPath(m.ConfigPath), row.Alias),
		hostIndex: row.Index,
	}
	m.mode = modeConfirm
	return m, nil
}

func (m *Model) deleteHost(index int) tea.Cmd {
	alias := ""
	key := ""
	if index >= 0 && index < len(m.cfg.Hosts) {
		alias = m.cfg.Hosts[index].PrimaryAlias()
		if resolved, err := m.cfg.Resolve(index); err == nil {
			key = keyring.Key(resolved.User, resolved.HostName, resolved.Port)
		}
	}
	if err := m.cfg.DeleteHost(index); err != nil {
		m.showError("删除 Host 失败", err.Error(), "按 r 重新加载后重试。")
		return nil
	}
	if err := m.cfg.Save(); err != nil {
		if cfg, loadErr := config.Load(m.ConfigPath); loadErr == nil {
			m.cfg = cfg
			m.refreshRows("")
		}
		m.showError("写入配置失败", err.Error(), "原文件未被覆盖，请检查权限后重试。")
		return nil
	}
	m.refreshRows("")
	if key == "" {
		m.setStatus("已删除 Host "+alias+"；无法解析目标，钥匙串密码未自动删除", true)
		return nil
	}
	m.setStatus("已删除 Host "+alias+"；正在删除对应系统钥匙串密码...", false)
	return m.deletePasswordCmd(key)
}

func (m *Model) confirmUnmount() (tea.Model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	raw := m.settings.MountPoint(row.Alias)
	path, err := mount.ExpandPath(raw)
	if err != nil {
		m.showError("挂载点无效", err.Error(), "按 s 检查挂载根目录设置。")
		return m, nil
	}
	mounted, err := m.checkMounted(path)
	if err != nil {
		m.showError("检查挂载状态失败", err.Error(), "请确认系统 mount 工具可用。")
		return m, nil
	}
	if !mounted {
		m.setStatus("Host "+row.Alias+" 当前未挂载（"+displayPath(path)+"）", false)
		return m, nil
	}
	m.confirm = &confirmDialog{
		kind:       confirmUnmount,
		title:      "卸载挂载点？",
		body:       fmt.Sprintf("将卸载 %s。\n\n请先关闭占用该目录的程序。", displayPath(path)),
		mountPoint: path,
	}
	m.mode = modeConfirm
	return m, nil
}

func (m *Model) openPasswordForm() (tea.Model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	resolved, err := m.cfg.Resolve(row.Index)
	if err != nil {
		m.showError("无法解析 Host", err.Error(), "修复 Host 后重试。")
		return m, nil
	}
	key := keyring.Key(resolved.User, resolved.HostName, resolved.Port)
	m.password = newPasswordForm(resolved.Alias, key, m.width)
	m.mode = modePassword
	return m, nil
}

func (m *Model) confirmDeletePassword() (tea.Model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	resolved, err := m.cfg.Resolve(row.Index)
	if err != nil {
		m.showError("无法解析 Host", err.Error(), "修复 Host 后重试。")
		return m, nil
	}
	key := keyring.Key(resolved.User, resolved.HostName, resolved.Port)
	m.confirm = &confirmDialog{
		kind:  confirmDeletePassword,
		title: "删除已保存的密码？",
		body:  fmt.Sprintf("将从系统钥匙串删除：\nservice=%s\nkey=%s\n\n此操作不可撤销。", keyring.Service, key),
		key:   key,
	}
	m.mode = modeConfirm
	return m, nil
}

func (m *Model) reloadAll() {
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		m.setStatus("重新加载配置失败："+err.Error(), true)
		return
	}
	st, err := settings.Load(m.SettingsPath)
	if err != nil {
		m.setStatus("重新加载设置失败："+err.Error(), true)
		return
	}
	alias := m.selectedAlias()
	m.cfg = cfg
	m.settings = st
	m.refreshRows(alias)
	m.setStatus("已重新加载配置和设置", false)
}

func (m *Model) forwardInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.mode {
	case modeSearch:
		m.search, cmd = m.search.Update(msg)
	case modeHostForm:
		if m.form != nil {
			m.form.inputs[m.form.focus], cmd = m.form.inputs[m.form.focus].Update(msg)
		}
	case modeSettings:
		if m.settingsForm != nil {
			switch m.settingsForm.focus {
			case settingMountRoot:
				m.settingsForm.mountRoot, cmd = m.settingsForm.mountRoot.Update(msg)
			case settingRemotePath:
				m.settingsForm.remotePath, cmd = m.settingsForm.remotePath.Update(msg)
			}
		}
	case modePassword:
		if m.password != nil {
			m.password.input, cmd = m.password.input.Update(msg)
		}
	}
	return cmd
}

func findConfigIndex(f *config.File, aliases []string) int {
	want := map[string]bool{}
	for _, a := range aliases {
		want[a] = true
	}
	for i, h := range f.Hosts {
		for _, a := range h.Aliases {
			if want[a] {
				return i
			}
		}
	}
	return -1
}

func mountFailureMessage(err error, output string, mounted bool, deps mount.DependencyReport) string {
	var parts []string
	switch {
	case err != nil:
		parts = append(parts, "sshfs 失败："+err.Error())
	case !mounted:
		parts = append(parts, "sshfs 已退出，但没有检测到挂载点。")
	}
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "permission denied"):
		parts = append(parts, "认证或远程路径权限不足。可按 r 改用远程目录 ~ 重试；也可以在设置中修改 remote_path，或为当前用户开放该目录权限。")
	case strings.Contains(lower, "host key verification failed"):
		parts = append(parts, "主机密钥校验失败，请先用 ssh 手动连接并确认 known_hosts。")
	case strings.Contains(lower, "fusermount") || strings.Contains(lower, "fuse"):
		parts = append(parts, "FUSE/sshfs 不可用，请检查 /dev/fuse 和 fusermount 安装。")
	case strings.Contains(lower, "no such file"):
		parts = append(parts, "远程路径不存在，请按 r 改用 ~ 或在设置中指定目录。")
	}
	for _, p := range deps.Problems {
		parts = append(parts, p)
	}
	return strings.Join(parts, "\n\n")
}

func sanitizeMountOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "password") || strings.Contains(lower, "passphrase") {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if len(out) > 1600 {
		out = out[len(out)-1600:]
	}
	return strings.TrimSpace(out)
}

func textInputBlink() tea.Cmd { return textinput.Blink }

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
