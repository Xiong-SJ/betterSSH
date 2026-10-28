// Package settings stores betterSSH-only preferences outside ~/.ssh/config.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ActionConnect only starts ssh.
	ActionConnect = "connect"
	// ActionMountConnect mounts with sshfs and then starts ssh.
	ActionMountConnect = "mount-connect"

	// OnMountedAsk asks the user what to do with an existing mount.
	OnMountedAsk = "ask"
	// OnMountedUnmount unmounts and mounts again.
	OnMountedUnmount = "unmount"
	// OnMountedUse reuses the existing mount.
	OnMountedUse = "use"
)

// Settings are user preferences. Per-host mount_points and remote_paths maps
// override the defaults.
type Settings struct {
	Version       int               `json:"version"`
	MountRoot     string            `json:"mount_root"`
	RemotePath    string            `json:"remote_path"`
	DefaultAction string            `json:"default_action"`
	OnMounted     string            `json:"on_mounted"`
	AutoUnmount   bool              `json:"auto_unmount"`
	StrictMount   bool              `json:"strict_mount"`
	MountPoints   map[string]string `json:"mount_points,omitempty"`
	RemotePaths   map[string]string `json:"remote_paths,omitempty"`
}

// Default returns the built-in settings.
func Default() Settings {
	return Settings{
		Version:       1,
		MountRoot:     "~/sshfs",
		RemotePath:    "/",
		DefaultAction: ActionConnect,
		OnMounted:     OnMountedAsk,
		MountPoints:   map[string]string{},
		RemotePaths:   map[string]string{},
	}
}

// DefaultPath returns ~/.config/betterSSH/settings.json, honoring
// XDG_CONFIG_HOME.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine home directory: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "betterSSH", "settings.json"), nil
}

// Load reads settings, filling missing fields with defaults.
func Load(path string) (Settings, error) {
	s := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("read settings: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Default(), fmt.Errorf("parse settings %s: %w", path, err)
	}
	s.applyDefaults()
	if err := s.Validate(); err != nil {
		return Default(), err
	}
	return s, nil
}

// Save atomically writes settings with 0600 permissions.
func Save(path string, s Settings) error {
	s.applyDefaults()
	if err := s.Validate(); err != nil {
		return err
	}
	if s.MountPoints == nil {
		s.MountPoints = map[string]string{}
	}
	if s.RemotePaths == nil {
		s.RemotePaths = map[string]string{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
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
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// MountPoint resolves the per-host mount point.
func (s Settings) MountPoint(alias string) string {
	if p := strings.TrimSpace(s.MountPoints[alias]); p != "" {
		return p
	}
	root := strings.TrimSpace(s.MountRoot)
	if root == "" {
		root = "~/sshfs"
	}
	if strings.Contains(root, "{alias}") {
		return strings.ReplaceAll(root, "{alias}", safeAlias(alias))
	}
	return filepath.Join(root, safeAlias(alias))
}

// RemotePathFor resolves the per-host remote path.
func (s Settings) RemotePathFor(alias string) string {
	if p := strings.TrimSpace(s.RemotePaths[alias]); p != "" {
		return p
	}
	if p := strings.TrimSpace(s.RemotePath); p != "" {
		return p
	}
	return "/"
}

// Validate checks enum values and required paths.
func (s *Settings) Validate() error {
	switch s.DefaultAction {
	case ActionConnect, ActionMountConnect:
	default:
		return fmt.Errorf("invalid default_action %q (want %q or %q)", s.DefaultAction, ActionConnect, ActionMountConnect)
	}
	switch s.OnMounted {
	case OnMountedAsk, OnMountedUnmount, OnMountedUse:
	default:
		return fmt.Errorf("invalid on_mounted %q", s.OnMounted)
	}
	if strings.TrimSpace(s.MountRoot) == "" {
		return errors.New("mount_root must not be empty")
	}
	if strings.TrimSpace(s.RemotePath) == "" {
		return errors.New("remote_path must not be empty")
	}
	return nil
}

func (s *Settings) applyDefaults() {
	d := Default()
	if s.Version == 0 {
		s.Version = d.Version
	}
	if strings.TrimSpace(s.MountRoot) == "" {
		s.MountRoot = d.MountRoot
	}
	if strings.TrimSpace(s.RemotePath) == "" {
		s.RemotePath = d.RemotePath
	}
	if s.DefaultAction == "" {
		s.DefaultAction = d.DefaultAction
	}
	if s.OnMounted == "" {
		s.OnMounted = d.OnMounted
	}
	for k, v := range s.MountPoints {
		if strings.TrimSpace(v) == "" {
			delete(s.MountPoints, k)
		}
	}
	for k, v := range s.RemotePaths {
		if strings.TrimSpace(v) == "" {
			delete(s.RemotePaths, k)
		}
	}
}

func safeAlias(alias string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "host"
	}
	var b strings.Builder
	for _, r := range alias {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == 0:
			b.WriteRune('_')
		case r < 0x20:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "." || out == ".." {
		return "host"
	}
	return out
}
