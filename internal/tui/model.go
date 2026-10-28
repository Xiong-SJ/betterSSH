// Package tui implements the Bubble Tea user interface.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"betterSSH/internal/config"
	"betterSSH/internal/keyring"
	"betterSSH/internal/mount"
	"betterSSH/internal/settings"
	"betterSSH/internal/version"
)

type mode int

const (
	modeList mode = iota
	modeSearch
	modeHostForm
	modeSettings
	modePassword
	modeConfirm
	modeError
	modeMountError
	modeMountExists
	modeSSHExit
	modeHelp
)

type hostRow struct {
	Index        int
	Aliases      []string
	Alias        string
	HostName     string
	User         string
	Port         int
	IdentityFile string
	ResolveErr   error
}

type connectPrep struct {
	Alias       string
	HostName    string
	User        string
	Port        int
	Identity    string
	ProxyJump   string
	WantMount   bool
	MountPoint  string
	RemotePath  string
	AskPass     bool
	AskPassKey  string
	AskPassWarn string
}

// Options are the runtime dependencies supplied by main.
type Options struct {
	ConfigPath       string
	SettingsPath     string
	Executable       string
	Store            keyring.Store
	AskPassSupported bool
	SSHVersion       string
	Dependencies     mount.DependencyReport
}

// Model is the complete TUI state.
type Model struct {
	ConfigPath   string
	SettingsPath string
	Executable   string
	Store        keyring.Store

	cfg      *config.File
	settings settings.Settings

	askPassSupported bool
	sshVersion       string
	deps             mount.DependencyReport
	isMounted        func(string) (bool, error)

	width  int
	height int

	mode    mode
	rows    []hostRow
	visible []int
	sel     int
	offset  int

	search textinput.Model
	status string
	isErr  bool
	busy   string

	form         *hostForm
	settingsForm *settingsForm
	password     *passwordForm
	confirm      *confirmDialog
	errDialog    *errorDialog
	mountFail    *mountFailure
	mountExists  *mountExisting
	sshExit      *sshExitPrompt
}

type preparedConnectMsg struct {
	prep    connectPrep
	mounted bool
	err     error
}

type mountFinishedMsg struct {
	prep    connectPrep
	output  string
	mounted bool
	err     error
}

type sshFinishedMsg struct {
	prep connectPrep
	err  error
}

type unmountedMsg struct {
	path    string
	prep    *connectPrep
	remount bool
	err     error
}

type passwordSavedMsg struct {
	key string
	err error
}

type passwordDeletedMsg struct {
	key   string
	found bool
	err   error
}

// New loads config and settings and returns an interactive model.
func New(opts Options) (*Model, error) {
	if opts.Store == nil {
		opts.Store = keyring.System{}
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	st, err := settings.Load(opts.SettingsPath)
	if err != nil {
		return nil, err
	}
	m := &Model{
		ConfigPath:       opts.ConfigPath,
		SettingsPath:     opts.SettingsPath,
		Executable:       opts.Executable,
		Store:            opts.Store,
		cfg:              cfg,
		settings:         st,
		askPassSupported: opts.AskPassSupported,
		sshVersion:       opts.SSHVersion,
		deps:             opts.Dependencies,
		isMounted:        mount.IsMounted,
		width:            100,
		height:           30,
		mode:             modeList,
	}
	m.search = textinput.New()
	m.search.Placeholder = "别名、主机、用户、IdentityFile"
	m.search.Prompt = "/ "
	m.search.CharLimit = 128
	m.search.Width = 60
	m.refreshRows("")
	if len(m.deps.Problems) > 0 {
		m.setStatus(fmt.Sprintf("依赖检查发现 %d 个问题，按 ? 查看详情", len(m.deps.Problems)), true)
	}
	return m, nil
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) refreshRows(selectAlias string) {
	entries := m.cfg.HostEntries()
	rows := make([]hostRow, 0, len(entries))
	for _, h := range entries {
		resolved, err := m.cfg.Resolve(h.Index)
		row := hostRow{
			Index:        h.Index,
			Aliases:      append([]string(nil), h.Aliases...),
			Alias:        h.PrimaryAlias(),
			HostName:     h.Field("HostName"),
			User:         h.Field("User"),
			IdentityFile: h.Field("IdentityFile"),
			Port:         config.DefaultPort,
			ResolveErr:   err,
		}
		if err == nil {
			row.Alias = resolved.Alias
			row.HostName = resolved.HostName
			row.User = resolved.User
			row.Port = resolved.Port
			row.IdentityFile = resolved.IdentityFile
		} else if row.HostName == "" {
			row.HostName = row.Alias
		}
		rows = append(rows, row)
	}
	m.rows = rows
	m.applyFilter()
	if selectAlias != "" {
		if i := m.findRow(selectAlias); i >= 0 {
			m.sel = i
		}
	}
	m.clampSelection()
}

func (m *Model) applyFilter() {
	q := strings.TrimSpace(m.search.Value())
	m.visible = m.visible[:0]
	for i, row := range m.rows {
		if matchRow(q, row) {
			m.visible = append(m.visible, i)
		}
	}
	m.clampSelection()
}

func (m *Model) clampSelection() {
	if len(m.visible) == 0 {
		m.sel = 0
		m.offset = 0
		return
	}
	if m.sel >= len(m.visible) {
		m.sel = len(m.visible) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
	m.ensureVisible()
}

func (m *Model) ensureVisible() {
	if len(m.visible) == 0 {
		m.offset = 0
		return
	}
	page := m.pageSize()
	if m.sel < m.offset {
		m.offset = m.sel
	}
	if m.sel >= m.offset+page {
		m.offset = m.sel - page + 1
	}
	maxOffset := len(m.visible) - page
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) pageSize() int {
	n := m.height - 9
	if n < 1 {
		n = 1
	}
	return n
}

func (m *Model) moveSelection(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.sel += delta
	m.clampSelection()
}

func (m *Model) selectedRow() (hostRow, bool) {
	if m.sel < 0 || m.sel >= len(m.visible) {
		return hostRow{}, false
	}
	return m.rows[m.visible[m.sel]], true
}

func (m *Model) selectedAlias() string {
	if row, ok := m.selectedRow(); ok {
		return row.Alias
	}
	return ""
}

func (m *Model) findRow(alias string) int {
	for i, row := range m.rows {
		for _, a := range row.Aliases {
			if a == alias {
				return i
			}
		}
		if row.Alias == alias {
			return i
		}
	}
	return -1
}

func (m *Model) setStatus(s string, isErr bool) {
	m.status = s
	m.isErr = isErr
}

func (m *Model) showError(title, message, hint string) {
	m.errDialog = &errorDialog{Title: title, Message: message, Hint: hint}
	m.mode = modeError
}

func matchRow(query string, row hostRow) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	haystacks := []string{
		row.Alias, strings.Join(row.Aliases, " "), row.HostName, row.User,
		row.IdentityFile, fmt.Sprintf("%d", row.Port),
	}
	for _, token := range strings.Fields(query) {
		matched := false
		for _, h := range haystacks {
			if fuzzyMatch(token, h) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// fuzzyMatch is a subsequence matcher: "exmpl" matches "example.com".
func fuzzyMatch(needle, haystack string) bool {
	needle = strings.ToLower(needle)
	haystack = strings.ToLower(haystack)
	if needle == "" {
		return true
	}
	pi := 0
	pr := []rune(needle)
	for _, r := range haystack {
		if r == pr[pi] {
			pi++
			if pi == len(pr) {
				return true
			}
		}
	}
	return false
}

func (m *Model) checkMounted(path string) (bool, error) {
	if m.isMounted != nil {
		return m.isMounted(path)
	}
	return mount.IsMounted(path)
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~/" + strings.TrimPrefix(path, home+string(filepath.Separator))
	}
	return path
}

func versionString() string { return "betterSSH " + version.Version }
