// Command betterSSH is a safe SSH config manager with an sshfs-aware TUI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"betterSSH/internal/config"
	"betterSSH/internal/keyring"
	"betterSSH/internal/mount"
	"betterSSH/internal/settings"
	sshutil "betterSSH/internal/ssh"
	"betterSSH/internal/tui"
	"betterSSH/internal/version"
)

// passwordStore is injectable for the AskPass dispatch tests. Production builds
// always use the operating system keyring.
var passwordStore keyring.Store = keyring.System{}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// This check must come before flag parsing: OpenSSH passes the prompt as an
	// argument and the helper must only print the password to stdout.
	if sshutil.IsAskPassMode(os.Getenv) || (len(args) > 0 && args[0] == "askpass") {
		if err := sshutil.RunAskPass(os.Getenv, passwordStore, stdout); err != nil {
			// Deliberately generic: never include keyring details or secrets.
			fmt.Fprintln(stderr, "betterSSH askpass: saved password unavailable")
			return 1
		}
		return 0
	}

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			printHelp(stdout)
			return 0
		case "-v", "--version", "version":
			fmt.Fprintln(stdout, "betterSSH", version.Version)
			return 0
		case "list", "ls":
			return cmdList(args[1:], stdout, stderr)
		case "show", "get":
			return cmdShow(args[1:], stdin, stdout, stderr)
		case "connect", "ssh":
			return cmdConnect(args[1:], stdin, stdout, stderr)
		case "mount":
			return cmdMount(args[1:], stdin, stdout, stderr)
		case "unmount", "umount":
			return cmdUnmount(args[1:], stdin, stdout, stderr)
		case "add":
			return cmdAdd(args[1:], stdin, stdout, stderr)
		case "edit":
			return cmdEdit(args[1:], stdin, stdout, stderr)
		case "remove", "rm", "delete":
			return cmdRemove(args[1:], stdin, stdout, stderr)
		case "tui":
			// Continue to TUI.
		default:
			fmt.Fprintf(stderr, "betterSSH: unknown command %q\n\n", args[0])
			printHelp(stderr)
			return 2
		}
	}

	configPath, err := config.DefaultPath()
	if err != nil {
		fmt.Fprintln(stderr, "betterSSH:", err)
		return 1
	}
	settingsPath, err := settings.DefaultPath()
	if err != nil {
		fmt.Fprintln(stderr, "betterSSH:", err)
		return 1
	}

	executable, _ := os.Executable()
	if executable != "" {
		executable, _ = filepath.Abs(executable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	sshVersion, askPassSupported, _ := sshutil.AskPassSupport(ctx, "ssh")
	cancel()

	model, err := tui.New(tui.Options{
		ConfigPath:       configPath,
		SettingsPath:     settingsPath,
		Executable:       executable,
		Store:            passwordStore,
		AskPassSupported: askPassSupported,
		SSHVersion:       sshVersion,
		Dependencies:     mount.CheckDependencies(),
	})
	if err != nil {
		fmt.Fprintln(stderr, "betterSSH:", err)
		return 1
	}
	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		if errors.Is(err, tea.ErrProgramKilled) {
			return 130
		}
		fmt.Fprintln(stderr, "betterSSH:", err)
		return 1
	}
	return 0
}

func listHosts(w io.Writer) error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tHOSTNAME\tUSER\tPORT\tIDENTITYFILE")
	for _, h := range cfg.HostEntries() {
		r, err := cfg.Resolve(h.Index)
		if err != nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", h.PrimaryAlias(), h.Field("HostName"), h.Field("User"), config.DefaultPort, h.Field("IdentityFile"))
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", r.Alias, r.HostName, r.User, r.Port, r.IdentityFile)
	}
	return tw.Flush()
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `betterSSH - SSH config manager with sshfs-aware TUI

Usage:
  betterSSH                         Start the TUI (default)
  betterSSH tui                     Start the TUI explicitly
  betterSSH list                    Print configured Hosts
  betterSSH show <alias>            Show resolved fields and mount state
  betterSSH connect <alias>         Connect (default action from settings)
  betterSSH mount <alias>           Mount with sshfs and keep it mounted
  betterSSH unmount <alias>         Unmount (confirmation unless --yes)
  betterSSH add <alias> [flags]     Add a Host
  betterSSH edit <alias> [flags]    Edit fields (confirmation unless --yes)
  betterSSH remove <alias> [--yes]  Delete a Host (confirmation unless --yes)
  betterSSH version                 Print the version
  betterSSH help                    Show this help

Quick connect examples:
  betterSSH connect prod --mount --unmount-after
  betterSSH connect prod --no-mount
  betterSSH mount prod --remote /srv/app --mountpoint ~/mnt/prod
  betterSSH unmount prod --yes

Host field flags for add/edit:
  --hostname, --user, --port, --identity-file, --proxy-jump, --server-alive

Safety:
  edit/remove/unmount ask for confirmation; add --yes for scripts.
  config writes back up to config.bak and rejects concurrent changes.
  Passwords are not accepted as CLI flags; use the TUI to save them.
  Passwords remain only in the OS keyring (service "betterSSH").
  No sshpass; no password in argv, environment, config files, logs, or database.

AskPass helper mode (used internally by OpenSSH):
  SSH_ENHANCED_ASKPASS=1 SSH_ENHANCED_ASKPASS_KEY=<key> betterSSH askpass
`)
}
