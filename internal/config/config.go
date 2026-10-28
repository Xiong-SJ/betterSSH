// Package config reads and writes OpenSSH client configuration files.
//
// The parser keeps the original line structure so edits can be applied in a
// block-aware, non-destructive way: comments, blank lines, ordering and
// directives that betterSSH does not understand stay untouched.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/kevinburke/ssh_config"
)

// DefaultPort is OpenSSH's default port.
const DefaultPort = 22

// ManagedKeys are the directives exposed by the TUI editor. Every other
// directive is preserved verbatim.
var ManagedKeys = []string{
	"HostName",
	"User",
	"Port",
	"IdentityFile",
	"ProxyJump",
	"ServerAliveInterval",
}

// Field is one parsed directive inside a Host block.
type Field struct {
	Key     string
	Value   string
	Line    int
	Indent  string
	Comment string // includes the leading '#', if any
}

// Host is a parsed Host block. Start is the line of the Host directive and End
// is one past the last directive in the block. Trailing blank lines and
// comments after the last directive are deliberately kept outside [Start,End)
// so deleting a host does not remove comments that may belong to the next one.
type Host struct {
	Index   int
	Start   int
	End     int
	Aliases []string
	Fields  []Field
	Comment string
}

// File is a parsed SSH config file together with its on-disk metadata.
type File struct {
	Path  string
	Lines []string
	Hosts []Host

	newline         string
	hadFinalNewline bool
	bom             string
	original        []byte
	mode            fs.FileMode
	exists          bool
	writePath       string

	decoded    *ssh_config.Config
	decodeErr  error
	decodeOnce bool
}

// HostInput is the editable representation of a Host block. Empty managed
// fields mean "remove this directive from the block".
type HostInput struct {
	Aliases             []string
	HostName            string
	User                string
	Port                string
	IdentityFile        string
	ProxyJump           string
	ServerAliveInterval string
}

// ResolvedHost contains effective SSH settings for an alias. Values inherited
// from a wildcard Host block are included when the config parser can determine
// them.
type ResolvedHost struct {
	Index               int
	Aliases             []string
	Alias               string
	HostName            string
	User                string
	Port                int
	IdentityFile        string
	ProxyJump           string
	ServerAliveInterval string
}

// ErrModified is returned when the file changed on disk since it was loaded.
var ErrModified = errors.New("ssh config was modified by another program; reload before saving")

// DefaultPath returns ~/.ssh/config.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// Load reads path. A missing file yields an empty, writable config so that the
// first "new host" operation can create it.
func Load(path string) (*File, error) {
	f := &File{
		Path:      path,
		writePath: path,
		newline:   "\n",
		mode:      0o600,
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("resolve symlink %s: %w", path, err)
		}
		f.writePath = resolved
	}
	data, err := os.ReadFile(f.writePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	parsed, err := Parse(path, data)
	if err != nil {
		return nil, err
	}
	// Copy parsed state, but keep the resolved write target and the original
	// bytes used for backup and conflict detection.
	f.Lines = parsed.Lines
	f.Hosts = parsed.Hosts
	f.newline = parsed.newline
	f.hadFinalNewline = parsed.hadFinalNewline
	f.bom = parsed.bom
	f.original = append([]byte(nil), data...)
	f.mode = info.Mode().Perm()
	f.exists = true
	return f, nil
}

// Parse parses data without touching disk.
func Parse(path string, data []byte) (*File, error) {
	f := &File{Path: path, writePath: path, newline: "\n", mode: 0o600}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		f.bom = string(data[:3])
		data = data[3:]
	}
	s := string(data)
	if strings.Contains(s, "\r\n") {
		f.newline = "\r\n"
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	f.hadFinalNewline = strings.HasSuffix(s, "\n")
	if f.hadFinalNewline {
		s = strings.TrimSuffix(s, "\n")
	}
	if s != "" {
		f.Lines = strings.Split(s, "\n")
	} else if f.hadFinalNewline {
		// A file containing exactly one empty line must not collapse to an
		// empty file when rendered again.
		f.Lines = []string{""}
	}
	f.reindex()
	return f, nil
}

// Render returns the current contents including the original BOM and newline
// style.
func (f *File) Render() []byte {
	var b strings.Builder
	b.WriteString(f.bom)
	b.WriteString(strings.Join(f.Lines, f.newline))
	if f.hadFinalNewline && len(f.Lines) > 0 {
		b.WriteString(f.newline)
	}
	return []byte(b.String())
}

// BackupPath is the conventional sidecar backup path.
func (f *File) BackupPath() string { return f.Path + ".bak" }

// HostEntries returns blocks that have at least one concrete alias. Wildcard
// and negation-only blocks (for example "Host *") are still preserved in the
// file but are not offered as connection targets.
func (f *File) HostEntries() []Host {
	out := make([]Host, 0, len(f.Hosts))
	for _, h := range f.Hosts {
		if h.PrimaryAlias() != "" {
			out = append(out, h)
		}
	}
	return out
}

// PrimaryAlias returns the first non-pattern alias in the block.
func (h Host) PrimaryAlias() string { return PrimaryAlias(h.Aliases) }

// PrimaryAlias returns the first concrete alias, or "" if there is none.
func PrimaryAlias(aliases []string) string {
	for _, a := range aliases {
		if IsConcreteAlias(a) {
			return a
		}
	}
	return ""
}

// Field returns the first value for key, case-insensitively.
func (h Host) Field(key string) string {
	for _, f := range h.Fields {
		if strings.EqualFold(f.Key, key) {
			return unquote(f.Value)
		}
	}
	return ""
}

// IsConcreteAlias reports whether alias is a literal host name rather than an
// OpenSSH pattern.
func IsConcreteAlias(alias string) bool {
	if alias == "" || strings.HasPrefix(alias, "!") {
		return false
	}
	return !strings.ContainsAny(alias, "*?[]")
}

// InputFromHost converts a parsed Host to editable form fields.
func InputFromHost(h Host) HostInput {
	return HostInput{
		Aliases:             append([]string(nil), h.Aliases...),
		HostName:            h.Field("HostName"),
		User:                h.Field("User"),
		Port:                h.Field("Port"),
		IdentityFile:        h.Field("IdentityFile"),
		ProxyJump:           h.Field("ProxyJump"),
		ServerAliveInterval: h.Field("ServerAliveInterval"),
	}
}

// ValidateInput normalizes and validates user input.
func ValidateInput(in *HostInput) error {
	if in == nil {
		return errors.New("empty host")
	}
	var aliases []string
	for _, a := range in.Aliases {
		for _, piece := range strings.Fields(a) {
			if piece == "" {
				continue
			}
			if strings.ContainsAny(piece, "#\"'\t\n\r") {
				return fmt.Errorf("invalid character in Host alias %q", piece)
			}
			if strings.HasPrefix(piece, "-") {
				return fmt.Errorf("Host alias %q must not start with '-'", piece)
			}
			aliases = append(aliases, piece)
		}
	}
	if len(aliases) == 0 {
		return errors.New("Host alias is required")
	}
	seen := map[string]bool{}
	var unique []string
	for _, a := range aliases {
		if seen[strings.ToLower(a)] {
			return fmt.Errorf("duplicate alias %q", a)
		}
		seen[strings.ToLower(a)] = true
		unique = append(unique, a)
	}
	in.Aliases = unique
	hasConcrete := false
	for _, a := range unique {
		if IsConcreteAlias(a) {
			hasConcrete = true
			break
		}
	}
	if !hasConcrete {
		return errors.New("at least one concrete (non-wildcard) Host alias is required")
	}

	trim := func(s string) string { return strings.TrimSpace(s) }
	in.HostName = trim(in.HostName)
	in.User = trim(in.User)
	in.Port = trim(in.Port)
	in.IdentityFile = trim(in.IdentityFile)
	in.ProxyJump = trim(in.ProxyJump)
	in.ServerAliveInterval = trim(in.ServerAliveInterval)

	for name, value := range map[string]string{
		"HostName": in.HostName, "User": in.User,
		"IdentityFile": in.IdentityFile, "ProxyJump": in.ProxyJump,
		"Port": in.Port, "ServerAliveInterval": in.ServerAliveInterval,
	} {
		if strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("%s contains a newline or NUL byte", name)
		}
	}
	if strings.ContainsAny(in.HostName, " \t") || strings.Contains(in.HostName, "#") {
		return errors.New("HostName must not contain whitespace or '#'")
	}
	if strings.ContainsAny(in.User, " \t@") || strings.Contains(in.User, "#") {
		return errors.New("User must not contain whitespace, '@' or '#'")
	}
	if in.Port != "" {
		p, err := strconv.Atoi(in.Port)
		if err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("Port must be an integer between 1 and 65535")
		}
	}
	if in.ServerAliveInterval != "" {
		n, err := strconv.Atoi(in.ServerAliveInterval)
		if err != nil || n < 0 || n > 86400 {
			return errors.New("ServerAliveInterval must be an integer between 0 and 86400")
		}
	}
	return nil
}

// AddHost appends a new Host block.
func (f *File) AddHost(in HostInput) error {
	if err := ValidateInput(&in); err != nil {
		return err
	}
	if err := f.checkAliasConflict(in.Aliases, -1); err != nil {
		return err
	}
	if len(f.Lines) > 0 && strings.TrimSpace(f.Lines[len(f.Lines)-1]) != "" {
		f.Lines = append(f.Lines, "")
	}
	f.Lines = append(f.Lines, "Host "+strings.Join(in.Aliases, " "))
	for _, key := range ManagedKeys {
		if v := inputValue(in, key); v != "" {
			f.Lines = append(f.Lines, "    "+key+" "+formatValue(v))
		}
	}
	f.hadFinalNewline = true
	f.reindex()
	return nil
}

// UpdateHost replaces managed directives in an existing block. Unknown
// directives, comments and the place of existing managed fields are retained.
func (f *File) UpdateHost(index int, in HostInput) error {
	if err := ValidateInput(&in); err != nil {
		return err
	}
	h, err := f.hostAt(index)
	if err != nil {
		return err
	}
	if err := f.checkAliasConflict(in.Aliases, index); err != nil {
		return err
	}

	indent := leadingWhitespace(f.Lines[h.Start])
	hostLine := indent + "Host " + strings.Join(in.Aliases, " ")
	if h.Comment != "" {
		hostLine += " " + h.Comment
	}

	replace := map[int]string{h.Start: hostLine}
	remove := map[int]bool{}
	var inserts []string
	for _, key := range ManagedKeys {
		value := inputValue(in, key)
		var matches []Field
		for _, fld := range h.Fields {
			if strings.EqualFold(fld.Key, key) {
				matches = append(matches, fld)
			}
		}
		if value == "" {
			for _, fld := range matches {
				remove[fld.Line] = true
			}
			continue
		}
		if len(matches) == 0 {
			inserts = append(inserts, "    "+key+" "+formatValue(value))
			continue
		}
		fld := matches[0]
		line := fld.Indent + fld.Key + " " + formatValue(value)
		if fld.Comment != "" {
			line += " " + fld.Comment
		}
		replace[fld.Line] = line
	}

	insertAt := h.End
	f.Lines = applyChanges(f.Lines, replace, remove, insertAt, inserts)
	f.reindex()
	return nil
}

// DeleteHost removes the Host directive and its directives, while preserving
// comments and blank lines that follow the last directive.
func (f *File) DeleteHost(index int) error {
	h, err := f.hostAt(index)
	if err != nil {
		return err
	}
	f.Lines = append(f.Lines[:h.Start], f.Lines[h.End:]...)
	f.reindex()
	return nil
}

// Resolve returns effective values for a host. It uses kevinburke/ssh_config
// for wildcard/default resolution and falls back to directives in the block if
// that parser cannot handle a construct (for example Match).
func (f *File) Resolve(index int) (ResolvedHost, error) {
	h, err := f.hostAt(index)
	if err != nil {
		return ResolvedHost{}, err
	}
	alias := h.PrimaryAlias()
	if alias == "" {
		return ResolvedHost{}, errors.New("host has no concrete alias")
	}
	r := ResolvedHost{
		Index:   index,
		Aliases: append([]string(nil), h.Aliases...),
		Alias:   alias,
	}
	get := func(key string) string {
		if v, err := f.effective(alias, key); err == nil && v != "" {
			return unquote(v)
		}
		return h.Field(key)
	}
	r.HostName = get("HostName")
	if r.HostName == "" {
		r.HostName = alias
	}
	r.User = get("User")
	if r.User == "" {
		r.User = currentUser()
	}
	r.IdentityFile = get("IdentityFile")
	r.ProxyJump = get("ProxyJump")
	r.ServerAliveInterval = get("ServerAliveInterval")
	r.Port = DefaultPort
	if p := get("Port"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 && n <= 65535 {
			r.Port = n
		}
	}
	return r, nil
}

// Save creates config.bak and atomically replaces the config. The original file
// is left untouched if any step fails.
func (f *File) Save() error {
	rendered := f.Render()
	if f.exists {
		current, err := os.ReadFile(f.writePath)
		if err != nil {
			return fmt.Errorf("re-read %s: %w", f.Path, err)
		}
		if !bytes.Equal(current, f.original) {
			return ErrModified
		}
		if err := atomicWrite(f.BackupPath(), f.original, 0o600); err != nil {
			return fmt.Errorf("write backup %s: %w", f.BackupPath(), err)
		}
		mode := f.mode.Perm()
		if mode == 0 {
			mode = 0o600
		}
		if err := atomicWrite(f.writePath, rendered, mode); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(f.writePath), 0o700); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
		if err := atomicWrite(f.writePath, rendered, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
		f.mode = 0o600
	}
	f.original = append([]byte(nil), rendered...)
	f.exists = true
	return nil
}

func (f *File) hostAt(index int) (Host, error) {
	if index < 0 || index >= len(f.Hosts) {
		return Host{}, fmt.Errorf("host index %d out of range", index)
	}
	return f.Hosts[index], nil
}

func (f *File) checkAliasConflict(aliases []string, except int) error {
	want := map[string]bool{}
	for _, a := range aliases {
		want[strings.ToLower(a)] = true
	}
	for i, h := range f.Hosts {
		if i == except {
			continue
		}
		for _, a := range h.Aliases {
			if want[strings.ToLower(a)] {
				return fmt.Errorf("Host alias %q already exists", a)
			}
		}
	}
	return nil
}

func (f *File) reindex() {
	f.decoded = nil
	f.decodeErr = nil
	f.decodeOnce = false
	f.Hosts = f.Hosts[:0]
	var current *Host
	lastDirective := -1
	finish := func() {
		if current == nil {
			return
		}
		end := lastDirective + 1
		if end <= current.Start {
			end = current.Start + 1
		}
		current.End = end
		f.Hosts = append(f.Hosts, *current)
		current = nil
	}
	for i, line := range f.Lines {
		key, value, comment, ok := parseDirective(line)
		if ok && strings.EqualFold(key, "Host") {
			finish()
			current = &Host{
				Index:   len(f.Hosts),
				Start:   i,
				Aliases: strings.Fields(value),
				Comment: comment,
			}
			lastDirective = i
			continue
		}
		if ok && strings.EqualFold(key, "Match") {
			finish()
			lastDirective = -1
			continue
		}
		if current != nil && ok {
			current.Fields = append(current.Fields, Field{
				Key:     key,
				Value:   value,
				Line:    i,
				Indent:  leadingWhitespace(line),
				Comment: comment,
			})
			lastDirective = i
		}
	}
	finish()
}

func (f *File) effective(alias, key string) (value string, err error) {
	if !f.decodeOnce {
		data := f.Render()
		if f.bom != "" {
			data = data[len(f.bom):]
		}
		f.decoded, f.decodeErr = ssh_config.Decode(bytes.NewReader(data))
		f.decodeOnce = true
	}
	if f.decodeErr != nil {
		return "", f.decodeErr
	}
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("parse effective config: %v", rec)
			value = ""
		}
	}()
	return f.decoded.Get(alias, key)
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "root"
}

func parseDirective(line string) (key, value, comment string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", "", false
	}
	i := 0
	for i < len(trimmed) && !unicode.IsSpace(rune(trimmed[i])) && trimmed[i] != '=' {
		i++
	}
	if i == 0 {
		return "", "", "", false
	}
	key = trimmed[:i]
	rest := trimmed[i:]
	rest = strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(rest, "=") {
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	value, comment = splitComment(rest)
	value = strings.TrimRight(value, " \t")
	return key, value, comment, true
}

// splitComment splits an inline comment from a directive value. A '#' inside
// quotes, or immediately adjacent to non-space text, is kept as data.
func splitComment(s string) (value, comment string) {
	var quote byte
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '#' && (i == 0 || unicode.IsSpace(rune(s[i-1]))) {
			return s[:i], strings.TrimSpace(s[i:])
		}
	}
	return s, ""
}

func leadingWhitespace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			if v, err := strconv.Unquote(s); err == nil {
				return v
			}
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return strings.ReplaceAll(s[1:len(s)-1], `\'`, `'`)
		}
	}
	return s
}

func formatValue(v string) string {
	if strings.ContainsAny(v, " \t#\"'") {
		return strconv.Quote(v)
	}
	return v
}

func inputValue(in HostInput, key string) string {
	switch strings.ToLower(key) {
	case "hostname":
		return in.HostName
	case "user":
		return in.User
	case "port":
		return in.Port
	case "identityfile":
		return in.IdentityFile
	case "proxyjump":
		return in.ProxyJump
	case "serveraliveinterval":
		return in.ServerAliveInterval
	default:
		return ""
	}
}

func applyChanges(lines []string, replace map[int]string, remove map[int]bool, insertAt int, inserts []string) []string {
	out := make([]string, 0, len(lines)+len(inserts))
	for i, line := range lines {
		if i == insertAt {
			out = append(out, inserts...)
		}
		if remove[i] {
			continue
		}
		if v, ok := replace[i]; ok {
			line = v
		}
		out = append(out, line)
	}
	if insertAt >= len(lines) {
		out = append(out, inserts...)
	}
	return out
}

func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".betterSSH-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
