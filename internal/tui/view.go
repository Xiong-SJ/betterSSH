package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"betterSSH/internal/settings"
)

var (
	accentColor = lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#5EEAD4"}
	mutedColor  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	errColor    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	okColor     = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	selBG       = lipgloss.AdaptiveColor{Light: "#CCFBF1", Dark: "#134E4A"}

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	styleMuted    = lipgloss.NewStyle().Foreground(mutedColor)
	styleErr      = lipgloss.NewStyle().Foreground(errColor)
	styleOK       = lipgloss.NewStyle().Foreground(okColor)
	styleSelected = lipgloss.NewStyle().Background(selBG).Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F8FAFC"})
	styleModal    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).Padding(1, 2)
	styleDanger   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(errColor).Padding(1, 2)
	styleCode     = lipgloss.NewStyle().Foreground(mutedColor).PaddingLeft(2)
	styleFocused  = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
)

// View implements tea.Model.
func (m *Model) View() string {
	switch m.mode {
	case modeHostForm:
		return m.hostFormView()
	case modeSettings:
		return m.settingsView()
	case modePassword:
		return m.passwordView()
	case modeConfirm:
		return m.confirmView()
	case modeError:
		return m.errorView()
	case modeMountError:
		return m.mountErrorView()
	case modeMountExists:
		return m.mountExistsView()
	case modeSSHExit:
		return m.sshExitView()
	case modeHelp:
		return m.helpView()
	default:
		return m.listView()
	}
}

func (m *Model) listView() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("betterSSH"))
	b.WriteString(styleMuted.Render("  " + displayPath(m.ConfigPath)))
	if len(m.deps.Problems) > 0 {
		b.WriteString(styleErr.Render("  ⚠ 依赖问题"))
	}
	hostLabel := "hosts"
	if len(m.rows) == 1 {
		hostLabel = "host"
	}
	b.WriteString(styleMuted.Render(fmt.Sprintf("  %d %s", len(m.rows), hostLabel)))
	b.WriteString("\n")

	query := strings.TrimSpace(m.search.Value())
	if m.mode == modeSearch {
		b.WriteString(m.search.View())
	} else if query != "" {
		b.WriteString(styleOK.Render(fmt.Sprintf("搜索: %s (%d/%d)  Esc 清除", query, len(m.visible), len(m.rows))))
	} else {
		b.WriteString(styleMuted.Render("按 / 搜索 · Enter 连接 · m 挂载并连接"))
	}
	b.WriteString("\n")
	b.WriteString(m.mountDetailLine())
	b.WriteString("\n")
	b.WriteString(m.tableHeader())
	b.WriteString("\n")

	page := m.pageSize()
	if len(m.visible) == 0 {
		var msg string
		if len(m.rows) == 0 {
			msg = "还没有可管理的 Host。按 n 新建一个。"
		} else {
			msg = "没有匹配的 Host。按 Esc 清除搜索。"
		}
		b.WriteString(styleMuted.Render("  " + msg))
		b.WriteString("\n")
	} else {
		end := m.offset + page
		if end > len(m.visible) {
			end = len(m.visible)
		}
		for i := m.offset; i < end; i++ {
			b.WriteString(m.tableRow(m.rows[m.visible[i]], i == m.sel))
			b.WriteString("\n")
		}
	}

	status := m.status
	if m.busy != "" {
		status = "⋯ " + m.busy
	}
	if status == "" {
		status = "就绪"
	}
	if m.busy != "" {
		b.WriteString(styleOK.Render(truncate(status, m.width)))
	} else if m.isErr {
		b.WriteString(styleErr.Render(truncate(status, m.width)))
	} else {
		b.WriteString(styleMuted.Render(truncate(status, m.width)))
	}
	b.WriteString("\n")
	for _, line := range m.listHelpLines() {
		b.WriteString(styleMuted.Render(truncate(line, m.width)))
		b.WriteString("\n")
	}
	return b.String()
}

// mountDetailLine makes the local mount target visible before the mount action
// starts, instead of hiding it behind the full-screen sshfs/ssh handoff.
func (m *Model) mountDetailLine() string {
	row, ok := m.selectedRow()
	if !ok {
		return styleMuted.Render("挂载点: 选择 Host 后显示")
	}
	point := m.settings.MountPoint(row.Alias)
	remote := m.settings.RemotePathFor(row.Alias)
	return styleMuted.Render("挂载点: ") +
		styleOK.Render(displayPath(point)) +
		styleMuted.Render("   ←   "+row.Alias+":"+remote) +
		styleMuted.Render("   (m 挂载并连接)")
}

func (m *Model) listHelpLines() []string {
	enter := "Enter 连接"
	if m.settings.DefaultAction == settings.ActionMountConnect {
		enter = "Enter 挂载连接"
	}
	if m.busy != "" {
		return []string{"请稍候 · Ctrl+C 退出"}
	}
	return []string{
		enter + " · m 挂载连接 · e 编辑 · n 新建 · d 删除 · u 卸载",
		"s 设置 · p 保存密码 · x 删除密码 · / 搜索 · r 重载 · ? 帮助 · q 退出",
	}
}

type columns struct {
	alias, host, user, port, identity int
	showUser, showIdentity            bool
	hasUser, hasIdentity              bool
}

func (m *Model) columnWidths() columns {
	avail := m.width - 2
	if avail < 20 {
		avail = 20
	}
	var c columns
	if avail >= 58 {
		c.showIdentity = true
	}
	if avail >= 44 {
		c.showUser = true
	}
	c.alias = clamp(avail/7, 9, 18)
	c.port = 5
	if c.showUser {
		c.user = clamp(avail/8, 8, 14)
	}
	if !c.showUser {
		c.user = 0
	}
	if c.showIdentity {
		c.identity = avail - c.alias - c.port - (2 * 4) - c.user
		c.host = clamp(avail/3, 12, 30)
		c.identity = avail - c.alias - c.host - c.user - c.port - (2 * 4)
		if c.identity < 8 {
			c.identity = 8
			c.host = avail - c.alias - c.user - c.port - c.identity - (2 * 4)
		}
	} else {
		c.host = avail - c.alias - c.port - (2 * 2) - c.user
		if c.host < 8 {
			c.host = 8
		}
	}
	return c
}

func (m *Model) tableHeader() string {
	c := m.columnWidths()
	parts := []string{
		padCell("  ALIAS", c.alias),
		padCell("HOSTNAME", c.host),
	}
	if c.showUser {
		parts = append(parts, padCell("USER", c.user))
	}
	parts = append(parts, padCell("PORT", c.port))
	if c.showIdentity {
		parts = append(parts, padCell("IDENTITYFILE", c.identity))
	}
	return styleMuted.Render(strings.Join(parts, "  "))
}

func (m *Model) tableRow(row hostRow, selected bool) string {
	c := m.columnWidths()
	alias := row.Alias
	if len(row.Aliases) > 1 {
		alias = fmt.Sprintf("%s +%d", row.Alias, len(row.Aliases)-1)
	}
	parts := []string{
		padCell("  "+alias, c.alias),
		padCell(row.HostName, c.host),
	}
	if c.showUser {
		parts = append(parts, padCell(row.User, c.user))
	}
	parts = append(parts, padCell(fmt.Sprintf("%d", row.Port), c.port))
	if c.showIdentity {
		parts = append(parts, padCell(row.IdentityFile, c.identity))
	}
	line := strings.Join(parts, "  ")
	line = truncate(line, m.width-2)
	if selected {
		line = "> " + strings.TrimPrefix(line, "  ")
		line = truncate(line, m.width)
		return styleSelected.Width(m.width).Render(line)
	}
	return line
}

func (m *Model) hostFormView() string {
	f := m.form
	if f == nil {
		return m.listView()
	}
	var b strings.Builder
	title := "新建 Host"
	if f.editing {
		title = "编辑 Host"
	}
	b.WriteString(styleTitle.Render(title))
	b.WriteString(styleMuted.Render("  " + displayPath(m.ConfigPath)))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("常用字段写回 Host 块；未知字段、注释和顺序保持不变。留空表示删除该字段。"))
	b.WriteString("\n\n")

	labels := []string{
		"Host 别名", "HostName", "User", "Port",
		"IdentityFile", "ProxyJump", "ServerAliveInterval", "保存密码",
	}
	placeholders := []string{
		"例如: prod-web（空格分隔可多个）", f.inherited.HostName, f.inherited.User,
		fmt.Sprintf("%d", f.inherited.Port), f.inherited.IdentityFile, f.inherited.ProxyJump,
		f.inherited.ServerAliveInterval, "",
	}
	for i := 0; i < hostFieldCount; i++ {
		if i == fieldPassword {
			check := "[ ]"
			if f.savePassword {
				check = "[x]"
			}
			line := fmt.Sprintf("%s  %s 保存到系统钥匙串  Ctrl+S 切换", focusMark(i == f.focus), check)
			b.WriteString(styleLine(i == f.focus, line))
			b.WriteString("\n")
			if f.savePassword {
				b.WriteString(fmt.Sprintf("%s  %s\n", focusMark(false), f.inputs[i].View()))
			} else {
				b.WriteString(styleMuted.Render("          密码不会写入配置、argv、环境变量或日志\n"))
			}
			continue
		}
		value := f.inputs[i].View()
		if strings.TrimSpace(value) == "" {
			value = styleMuted.Render(placeholders[i])
		}
		line := fmt.Sprintf("%s  %-20s %s", focusMark(i == f.focus), labels[i], value)
		b.WriteString(styleLine(i == f.focus, line))
		b.WriteString("\n")
	}
	if f.err != "" {
		b.WriteString("\n")
		b.WriteString(styleErr.Render("✗ " + f.err))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("Tab/↑↓ 切换 · Ctrl+S 启用密码保存 · Enter 保存 · Esc 取消"))
	b.WriteString("\n")
	return b.String()
}

func (m *Model) settingsView() string {
	f := m.settingsForm
	if f == nil {
		return m.listView()
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("设置"))
	b.WriteString(styleMuted.Render("  " + displayPath(m.SettingsPath)))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("仅写入工具设置，不污染 ~/.ssh/config。per-host 覆盖可在 settings.json 的 mount_points/remote_paths 中配置。"))
	b.WriteString("\n\n")

	b.WriteString(m.settingLine(settingMountRoot, "默认挂载根目录", f.mountRoot.View()))
	b.WriteString(m.settingLine(settingRemotePath, "默认远程路径", f.remotePath.View()))
	b.WriteString(m.settingLine(settingDefaultAction, "Enter 默认动作", settingChoice(f.draft.DefaultAction)))
	b.WriteString(m.settingLine(settingOnMounted, "已挂载时", mountedChoice(f.draft.OnMounted)))
	b.WriteString(m.settingLine(settingAutoUnmount, "SSH 退出后自动卸载(不询问)", checkBox(f.draft.AutoUnmount)))
	b.WriteString(m.settingLine(settingStrictMount, "严格挂载模式", checkBox(f.draft.StrictMount)))
	b.WriteString("\n")
	if f.err != "" {
		b.WriteString(styleErr.Render("✗ " + f.err))
		b.WriteString("\n")
	}
	b.WriteString(styleMuted.Render("依赖: "))
	if m.deps.OK() {
		b.WriteString(styleOK.Render("ssh / sshfs / FUSE 正常"))
	} else {
		b.WriteString(styleErr.Render(strings.Join(m.deps.Problems, "；")))
	}
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("AskPass: "))
	if m.askPassSupported {
		b.WriteString(styleOK.Render("支持（" + shortVersion(m.sshVersion) + "）"))
	} else {
		b.WriteString(styleErr.Render("不可用，已保存密码将回退到终端输入"))
	}
	b.WriteString("\n\n")
	b.WriteString(styleMuted.Render("Tab/↑↓ 切换 · ←/→ 修改选项 · Space 切换开关 · Enter 保存 · Esc 取消"))
	b.WriteString("\n")
	return b.String()
}

func (m *Model) settingLine(idx int, label, value string) string {
	mark := focusMark(idx == m.settingsForm.focus)
	line := fmt.Sprintf("%s  %-24s %s", mark, label, value)
	return styleLine(idx == m.settingsForm.focus, line) + "\n"
}

func (m *Model) passwordView() string {
	p := m.password
	if p == nil {
		return m.listView()
	}
	body := fmt.Sprintf("为 Host %s 保存 SSH 密码。\n\nservice = %s\nkey     = %s\n\n%s\n\n%s",
		p.alias, "betterSSH", p.key, p.input.View(),
		styleMuted.Render("密码只写入操作系统钥匙串；不会写入 config、argv、环境变量或日志。"))
	if p.err != "" {
		body += "\n\n" + styleErr.Render("✗ "+p.err)
	}
	return m.modalCard("保存密码", body, "Enter 下一步 · Esc 取消", false)
}

func (m *Model) confirmView() string {
	c := m.confirm
	if c == nil {
		return m.listView()
	}
	body := c.body
	hint := "[Enter/y] 确认    [Esc/n] 取消"
	danger := false
	switch c.kind {
	case confirmDeleteHost, confirmUnmount, confirmDeletePassword:
		danger = true
		hint = "[Enter/y] 确认删除/卸载    [Esc/n] 取消"
	case confirmSaveHost, confirmSavePassword, confirmSaveSettings:
		hint = "[Enter/y] 确认写入    [Esc/n] 返回"
	}
	return m.modalCard(c.title, body, hint, danger)
}

func (m *Model) errorView() string {
	e := m.errDialog
	if e == nil {
		return m.listView()
	}
	body := e.Message
	if e.Hint != "" {
		body += "\n\n" + styleMuted.Render(e.Hint)
	}
	return m.modalCard(e.Title, body, "按 Enter / Esc 返回", true)
}

func (m *Model) mountErrorView() string {
	f := m.mountFail
	if f == nil {
		return m.listView()
	}
	body := fmt.Sprintf("Host: %s\n远程路径: %s\n本机挂载点: %s\n\n%s",
		f.prep.Alias, f.prep.RemotePath, displayPath(f.prep.MountPoint), f.message)
	if f.strict {
		body = styleErr.Render("严格挂载模式已开启：不会继续启动 ssh。") + "\n\n" + body
	}
	if f.output != "" {
		body += "\n\n" + styleMuted.Render("sshfs 输出（已过滤密码提示）:") + "\n" + styleCode.Render(tailLines(f.output, 12))
	}
	hint := "[Enter] 仍然连接    [r] 用远程 ~ 重试    [s] 设置    [Esc] 取消"
	if f.strict {
		hint = "[r] 用远程 ~ 重试    [s] 设置    [Esc] 取消（严格模式禁止继续连接）"
	}
	return m.modalCard("挂载失败 · "+f.prep.Alias, body, hint, true)
}

func (m *Model) sshExitView() string {
	e := m.sshExit
	if e == nil {
		return m.listView()
	}
	status := m.status
	if status == "" {
		status = "SSH 会话已结束。"
	}
	body := fmt.Sprintf("%s\n\nHost: %s\n本机挂载点: %s\n远程路径: %s\n\n是否继续保留这个目录挂载？",
		status, e.prep.Alias, displayPath(e.prep.MountPoint), e.prep.RemotePath)
	return m.modalCard("SSH 会话已结束", body,
		"[Enter/k] 继续挂载    [u] 卸载并释放    [Esc] 保持现状", false)
}

func (m *Model) mountExistsView() string {
	e := m.mountExists
	if e == nil {
		return m.listView()
	}
	body := fmt.Sprintf("Host %s 的挂载点已存在：\n\n%s\n\n远程路径: %s\n\n按设置需要确认如何处理。",
		e.prep.Alias, displayPath(e.prep.MountPoint), e.prep.RemotePath)
	return m.modalCard("挂载点已存在", body,
		"[u] 卸载后重新挂载    [c] 使用现有挂载并连接    [s] 设置    [Esc] 取消", false)
}

func (m *Model) helpView() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(versionString()))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("配置: " + displayPath(m.ConfigPath)))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("设置: " + displayPath(m.SettingsPath)))
	b.WriteString("\n\n")
	b.WriteString(styleTitle.Render("快捷键") + "\n")
	b.WriteString("Enter        按默认动作连接（当前: " + settingChoice(m.settings.DefaultAction) + "）\n")
	b.WriteString("m / Ctrl+M   挂载并连接\n")
	b.WriteString("e / n / d    编辑 / 新建 / 删除 Host（删除需确认）\n")
	b.WriteString("u            卸载选中 Host 的挂载点（需确认）\n")
	b.WriteString("SSH 退出后   询问继续挂载还是卸载（auto_unmount 开启时自动卸载）\n")
	b.WriteString("s / p / x    设置 / 保存密码 / 删除已保存密码\n")
	b.WriteString("/ / r / q    模糊搜索 / 重新加载 / 退出\n")
	b.WriteString("?            本帮助\n\n")
	b.WriteString(styleTitle.Render("安全") + "\n")
	b.WriteString("• 密码只保存在操作系统钥匙串，service=betterSSH\n")
	b.WriteString("• AskPass 环境变量只传 keyring key，不传密码\n")
	b.WriteString("• 不调用 sshpass，不将密码放入 argv/env/config/log\n")
	b.WriteString("• config 写回前自动生成 config.bak，并用原子替换\n")
	b.WriteString("• 删除、覆盖、卸载均需二次确认\n\n")
	b.WriteString(styleTitle.Render("依赖与限制") + "\n")
	if len(m.deps.Problems) == 0 {
		b.WriteString(styleOK.Render("• ssh / sshfs / FUSE 检查通过") + "\n")
	} else {
		for _, p := range m.deps.Problems {
			b.WriteString(styleErr.Render("• "+p) + "\n")
		}
	}
	if m.askPassSupported {
		b.WriteString(styleOK.Render("• AskPass 可用（"+shortVersion(m.sshVersion)+"）") + "\n")
	} else {
		b.WriteString(styleErr.Render("• AskPass 不可用（需要 OpenSSH 8.4+），密码将回退到终端输入") + "\n")
	}
	b.WriteString("• Include 引入的 Host 不会列出/编辑；Match 与未知字段原样保留\n")
	b.WriteString("• 终端通常无法区分 Ctrl+M 与 Enter；若需强制挂载请用 m\n")
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("按 Esc / Enter / q 返回"))
	return m.clipScreen(m.modalCard("帮助", b.String(), "", false))
}

func (m *Model) modalCard(title, body, hint string, danger bool) string {
	width := clamp(m.width-6, 30, 92)
	style := styleModal
	if danger {
		style = styleDanger
	}
	bodyStyle := lipgloss.NewStyle().Width(width - 6)
	content := styleTitle.Render(title) + "\n\n" + bodyStyle.Render(body)
	if hint != "" {
		content += "\n\n" + styleMuted.Render(hint)
	}
	card := style.Width(width).Render(content)
	return lipgloss.Place(maxInt(1, m.width), maxInt(1, m.height), lipgloss.Center, lipgloss.Center, card)
}

func (m *Model) clipScreen(s string) string {
	if m.height <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= m.height {
		return s
	}
	if m.height < 2 {
		return lines[0]
	}
	lines = lines[:m.height-1]
	lines = append(lines, styleMuted.Render("… 终端高度不足，请放大窗口"))
	return strings.Join(lines, "\n")
}

func focusMark(focused bool) string {
	if focused {
		return styleFocused.Render("›")
	}
	return " "
}

func styleLine(focused bool, line string) string {
	if focused {
		return styleFocused.Render(line)
	}
	return line
}

func settingChoice(v string) string {
	if v == settings.ActionMountConnect {
		return "挂载并连接 (mount-connect)"
	}
	return "仅连接 (connect)"
}

func mountedChoice(v string) string {
	switch v {
	case settings.OnMountedUnmount:
		return "先卸载再挂载 (unmount)"
	case settings.OnMountedUse:
		return "复用现有挂载 (use)"
	default:
		return "询问 (ask)"
	}
}

func checkBox(v bool) string {
	if v {
		return "[x] 开启"
	}
	return "[ ] 关闭"
}

func shortVersion(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return "OpenSSH"
	}
	return v
}

func padCell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.FillRight(runewidth.Truncate(s, w, "…"), w)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.Truncate(s, w, "…")
}

func tailLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
