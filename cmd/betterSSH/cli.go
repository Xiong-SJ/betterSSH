package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"betterSSH/internal/config"
	"betterSSH/internal/keyring"
	"betterSSH/internal/mount"
	"betterSSH/internal/settings"
	sshutil "betterSSH/internal/ssh"
)

type cliContext struct {
	stdin        io.Reader
	stdout       io.Writer
	stderr       io.Writer
	configPath   string
	settingsPath string
	executable   string
	cfg          *config.File
	settings     settings.Settings
}

type cliMountInfo struct {
	Point    string
	DidMount bool
}

type hostFlagValues struct {
	hostname string
	user     string
	port     string
	identity string
	proxy    string
	alive    string
}

func newCLIContext(stdin io.Reader, stdout, stderr io.Writer) (*cliContext, error) {
	configPath, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	settingsPath, err := settings.DefaultPath()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	st, err := settings.Load(settingsPath)
	if err != nil {
		return nil, err
	}
	exe, _ := os.Executable()
	if exe != "" {
		exe, _ = filepath.Abs(exe)
	}
	return &cliContext{
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		configPath:   configPath,
		settingsPath: settingsPath,
		executable:   exe,
		cfg:          cfg,
		settings:     st,
	}, nil
}

func (c *cliContext) findHost(alias string) (int, error) {
	for i, h := range c.cfg.Hosts {
		for _, a := range h.Aliases {
			if strings.EqualFold(a, alias) {
				return i, nil
			}
		}
	}
	return -1, fmt.Errorf("Host %q 不存在", alias)
}

func (c *cliContext) target(alias string) (int, config.ResolvedHost, sshutil.Target, error) {
	if !config.IsConcreteAlias(alias) {
		return -1, config.ResolvedHost{}, sshutil.Target{}, fmt.Errorf("%q 不是可连接的 Host 别名", alias)
	}
	idx, err := c.findHost(alias)
	if err != nil {
		return -1, config.ResolvedHost{}, sshutil.Target{}, err
	}
	resolved, err := c.cfg.Resolve(idx)
	if err != nil {
		return -1, config.ResolvedHost{}, sshutil.Target{}, err
	}
	return idx, resolved, sshutil.Target{
		Alias:        alias,
		HostName:     resolved.HostName,
		User:         resolved.User,
		Port:         resolved.Port,
		IdentityFile: resolved.IdentityFile,
		ProxyJump:    resolved.ProxyJump,
	}, nil
}

func newFlagSet(name, usage string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "用法: betterSSH %s\n", usage)
		fs.PrintDefaults()
	}
	return fs
}

func parseAliasFlags(fs *flag.FlagSet, args []string) (string, error) {
	var alias string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		alias = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if alias == "" {
		if fs.NArg() == 0 {
			return "", errors.New("缺少 Host 别名")
		}
		alias = fs.Arg(0)
		if fs.NArg() > 1 {
			return "", fmt.Errorf("多余的参数: %s", strings.Join(fs.Args()[1:], " "))
		}
	} else if fs.NArg() > 0 {
		return "", fmt.Errorf("多余的参数: %s", strings.Join(fs.Args(), " "))
	}
	return alias, nil
}

func flagError(stderr io.Writer, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(stderr, "betterSSH:", err)
	return 2
}

func setupError(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "betterSSH:", err)
	return 1
}

func registerHostFlags(fs *flag.FlagSet) *hostFlagValues {
	v := &hostFlagValues{}
	fs.StringVar(&v.hostname, "hostname", "", "HostName")
	fs.StringVar(&v.user, "user", "", "SSH 用户名")
	fs.StringVar(&v.port, "port", "", "SSH 端口")
	fs.StringVar(&v.identity, "identity-file", "", "IdentityFile")
	fs.StringVar(&v.proxy, "proxy-jump", "", "ProxyJump")
	fs.StringVar(&v.alive, "server-alive", "", "ServerAliveInterval")
	return v
}

func hostInputFrom(v *hostFlagValues, alias string) config.HostInput {
	return config.HostInput{
		Aliases:             []string{alias},
		HostName:            v.hostname,
		User:                v.user,
		Port:                v.port,
		IdentityFile:        v.identity,
		ProxyJump:           v.proxy,
		ServerAliveInterval: v.alive,
	}
}

func confirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprintf(out, "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func shortPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && path == home {
		return "~"
	}
	if err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~/" + strings.TrimPrefix(path, home+string(filepath.Separator))
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cmdList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("list", "list", stderr)
	if err := fs.Parse(args); err != nil {
		return flagError(stderr, err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "betterSSH: list 不接受额外参数")
		return 2
	}
	if err := listHosts(stdout); err != nil {
		return setupError(stderr, err)
	}
	return 0
}

func cmdShow(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("show", "<alias>", stderr)
	alias, err := parseAliasFlags(fs, args)
	if err != nil {
		return flagError(stderr, err)
	}
	c, err := newCLIContext(stdin, stdout, stderr)
	if err != nil {
		return setupError(stderr, err)
	}
	idx, err := c.findHost(alias)
	if err != nil {
		return setupError(stderr, err)
	}
	resolved, err := c.cfg.Resolve(idx)
	if err != nil {
		return setupError(stderr, err)
	}
	point, err := mount.ExpandPath(c.settings.MountPoint(alias))
	if err != nil {
		return setupError(stderr, err)
	}
	mounted := "unknown"
	if ok, err := mount.IsMounted(point); err == nil {
		if ok {
			mounted = "yes"
		} else {
			mounted = "no"
		}
	}
	h := c.cfg.Hosts[idx]
	fmt.Fprintf(stdout, "Host: %s\n", strings.Join(h.Aliases, " "))
	fmt.Fprintf(stdout, "HostName: %s\n", resolved.HostName)
	fmt.Fprintf(stdout, "User: %s\n", resolved.User)
	fmt.Fprintf(stdout, "Port: %d\n", resolved.Port)
	fmt.Fprintf(stdout, "IdentityFile: %s\n", emptyDash(resolved.IdentityFile))
	fmt.Fprintf(stdout, "ProxyJump: %s\n", emptyDash(resolved.ProxyJump))
	fmt.Fprintf(stdout, "ServerAliveInterval: %s\n", emptyDash(resolved.ServerAliveInterval))
	fmt.Fprintf(stdout, "MountPoint: %s\n", point)
	fmt.Fprintf(stdout, "RemotePath: %s\n", c.settings.RemotePathFor(alias))
	fmt.Fprintf(stdout, "Mounted: %s\n", mounted)
	fmt.Fprintf(stdout, "KeyringKey: %s\n", keyring.Key(resolved.User, resolved.HostName, resolved.Port))
	fmt.Fprintf(stdout, "Config: %s\n", shortPath(c.configPath))
	return 0
}

func cmdAdd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("add", "<alias> [字段选项]", stderr)
	v := registerHostFlags(fs)
	alias, err := parseAliasFlags(fs, args)
	if err != nil {
		return flagError(stderr, err)
	}
	c, err := newCLIContext(stdin, stdout, stderr)
	if err != nil {
		return setupError(stderr, err)
	}
	existed := fileExists(c.configPath)
	if err := c.cfg.AddHost(hostInputFrom(v, alias)); err != nil {
		return setupError(stderr, err)
	}
	if err := c.cfg.Save(); err != nil {
		return setupError(stderr, err)
	}
	fmt.Fprintf(stdout, "已新增 Host %s\n", alias)
	if existed {
		fmt.Fprintf(stdout, "配置已写入 %s；备份: %s\n", shortPath(c.configPath), shortPath(c.cfg.BackupPath()))
	} else {
		fmt.Fprintf(stdout, "配置已创建: %s\n", shortPath(c.configPath))
	}
	return 0
}

func cmdEdit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("edit", "<alias> [字段选项] [--yes]", stderr)
	v := registerHostFlags(fs)
	yes := fs.Bool("yes", false, "跳过确认")
	alias, err := parseAliasFlags(fs, args)
	if err != nil {
		return flagError(stderr, err)
	}
	c, err := newCLIContext(stdin, stdout, stderr)
	if err != nil {
		return setupError(stderr, err)
	}
	idx, err := c.findHost(alias)
	if err != nil {
		return setupError(stderr, err)
	}
	in := config.InputFromHost(c.cfg.Hosts[idx])
	changed := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "hostname":
			in.HostName = v.hostname
			changed = true
		case "user":
			in.User = v.user
			changed = true
		case "port":
			in.Port = v.port
			changed = true
		case "identity-file":
			in.IdentityFile = v.identity
			changed = true
		case "proxy-jump":
			in.ProxyJump = v.proxy
			changed = true
		case "server-alive":
			in.ServerAliveInterval = v.alive
			changed = true
		}
	})
	if !changed {
		return setupError(stderr, errors.New("没有指定要修改的字段"))
	}
	if !*yes {
		if !confirm(c.stdin, c.stdout, fmt.Sprintf("将覆盖 Host %s 的常用字段并生成 config.bak，继续?", alias)) {
			fmt.Fprintln(stdout, "已取消")
			return 0
		}
	}
	if err := c.cfg.UpdateHost(idx, in); err != nil {
		return setupError(stderr, err)
	}
	if err := c.cfg.Save(); err != nil {
		return setupError(stderr, err)
	}
	fmt.Fprintf(stdout, "已更新 Host %s\n", alias)
	fmt.Fprintf(stdout, "配置: %s；备份: %s\n", shortPath(c.configPath), shortPath(c.cfg.BackupPath()))
	return 0
}

func cmdRemove(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("remove", "<alias> [--yes]", stderr)
	yes := fs.Bool("yes", false, "跳过确认")
	alias, err := parseAliasFlags(fs, args)
	if err != nil {
		return flagError(stderr, err)
	}
	c, err := newCLIContext(stdin, stdout, stderr)
	if err != nil {
		return setupError(stderr, err)
	}
	idx, err := c.findHost(alias)
	if err != nil {
		return setupError(stderr, err)
	}
	key := ""
	if resolved, err := c.cfg.Resolve(idx); err == nil {
		key = keyring.Key(resolved.User, resolved.HostName, resolved.Port)
	}
	if !*yes {
		if !confirm(c.stdin, c.stdout, fmt.Sprintf("删除 Host %s 及其字段，并自动删除对应系统钥匙串密码?（将生成 config.bak）", alias)) {
			fmt.Fprintln(stdout, "已取消")
			return 0
		}
	}
	if err := c.cfg.DeleteHost(idx); err != nil {
		return setupError(stderr, err)
	}
	if err := c.cfg.Save(); err != nil {
		return setupError(stderr, err)
	}
	fmt.Fprintf(stdout, "已删除 Host %s\n", alias)
	fmt.Fprintf(stdout, "配置: %s；备份: %s\n", shortPath(c.configPath), shortPath(c.cfg.BackupPath()))
	if key == "" {
		fmt.Fprintln(stderr, "警告: 无法解析 User/HostName/Port，未自动删除钥匙串密码")
		return 0
	}
	found, err := deleteStoredPassword(key)
	if err != nil {
		fmt.Fprintf(stderr, "警告: Host 已删除，但删除系统钥匙串密码失败（%s）：%v\n", key, err)
		return 1
	}
	if found {
		fmt.Fprintf(stdout, "已删除系统钥匙串密码（%s）\n", key)
	} else {
		fmt.Fprintf(stdout, "未发现对应系统钥匙串密码（%s）\n", key)
	}
	return 0
}

func cmdUnmount(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("unmount", "<alias> [--mountpoint PATH] [--yes]", stderr)
	yes := fs.Bool("yes", false, "跳过确认")
	pointOverride := fs.String("mountpoint", "", "覆盖本机挂载点")
	alias, err := parseAliasFlags(fs, args)
	if err != nil {
		return flagError(stderr, err)
	}
	c, err := newCLIContext(stdin, stdout, stderr)
	if err != nil {
		return setupError(stderr, err)
	}
	raw := *pointOverride
	if raw == "" {
		raw = c.settings.MountPoint(alias)
	}
	point, err := mount.ExpandPath(raw)
	if err != nil {
		return setupError(stderr, err)
	}
	mounted, err := mount.IsMounted(point)
	if err != nil {
		return setupError(stderr, err)
	}
	if !mounted {
		fmt.Fprintf(stdout, "Host %s 当前未挂载（%s）\n", alias, shortPath(point))
		return 0
	}
	if !*yes {
		if !confirm(c.stdin, c.stdout, fmt.Sprintf("卸载 %s?", shortPath(point))) {
			fmt.Fprintln(stdout, "已取消")
			return 0
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := mount.Unmount(ctx, point); err != nil {
		return setupError(stderr, err)
	}
	fmt.Fprintf(stdout, "已卸载 %s\n", shortPath(point))
	return 0
}

func cmdMount(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("mount", "<alias> [--remote PATH] [--mountpoint PATH] [--remount]", stderr)
	remote := fs.String("remote", "", "覆盖远程路径")
	pointOverride := fs.String("mountpoint", "", "覆盖本机挂载点")
	remount := fs.Bool("remount", false, "若已挂载则先卸载再挂载")
	alias, err := parseAliasFlags(fs, args)
	if err != nil {
		return flagError(stderr, err)
	}
	c, err := newCLIContext(stdin, stdout, stderr)
	if err != nil {
		return setupError(stderr, err)
	}
	_, _, target, err := c.target(alias)
	if err != nil {
		return setupError(stderr, err)
	}
	point, err := c.mountPointFor(alias, *pointOverride)
	if err != nil {
		return setupError(stderr, err)
	}
	if *remount {
		if mounted, err := mount.IsMounted(point); err == nil && mounted {
			if err := c.unmountPoint(point); err != nil {
				return setupError(stderr, err)
			}
			fmt.Fprintf(stdout, "已卸载旧挂载 %s\n", shortPath(point))
		}
	}
	info, err := c.mountAlias(alias, target, *remote, point)
	if err != nil {
		return setupError(stderr, err)
	}
	if info.DidMount {
		fmt.Fprintf(stdout, "已挂载 %s:%s -> %s\n", remoteTarget(target), c.remotePathFor(alias, *remote), shortPath(info.Point))
	} else {
		fmt.Fprintf(stdout, "挂载点已存在并复用: %s\n", shortPath(info.Point))
	}
	return 0
}

func cmdConnect(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("connect", "<alias> [--mount|--no-mount] [--unmount-after] [--remote PATH] [--mountpoint PATH]", stderr)
	mountFlag := fs.Bool("mount", false, "强制 sshfs 挂载后连接")
	noMount := fs.Bool("no-mount", false, "仅连接，不挂载")
	unmountAfter := fs.Bool("unmount-after", false, "SSH 退出后卸载本次使用的挂载点")
	remote := fs.String("remote", "", "覆盖远程路径（配合挂载）")
	pointOverride := fs.String("mountpoint", "", "覆盖本机挂载点（配合挂载）")
	alias, err := parseAliasFlags(fs, args)
	if err != nil {
		return flagError(stderr, err)
	}
	if *mountFlag && *noMount {
		return flagError(stderr, errors.New("--mount 与 --no-mount 不能同时使用"))
	}
	c, err := newCLIContext(stdin, stdout, stderr)
	if err != nil {
		return setupError(stderr, err)
	}
	_, resolved, target, err := c.target(alias)
	if err != nil {
		return setupError(stderr, err)
	}
	if _, err := sshutil.FindTool("ssh"); err != nil {
		return setupError(stderr, fmt.Errorf("无法连接: %w", err))
	}

	wantMount := c.settings.DefaultAction == settings.ActionMountConnect
	if *mountFlag {
		wantMount = true
	}
	if *noMount {
		wantMount = false
	}

	mountPoint := ""
	didMount := false
	if wantMount {
		info, err := c.mountAlias(alias, target, *remote, *pointOverride)
		if err != nil {
			fmt.Fprintln(stderr, "挂载失败:", err)
			if c.settings.StrictMount {
				fmt.Fprintln(stderr, "严格挂载模式已开启，已中止 SSH 连接。")
				return 1
			}
			fmt.Fprintln(stderr, "警告: 挂载失败，按当前设置继续仅启动 SSH。")
		} else {
			mountPoint = info.Point
			didMount = info.DidMount
			if info.DidMount {
				fmt.Fprintf(stdout, "已挂载 %s:%s -> %s\n", remoteTarget(target), c.remotePathFor(alias, *remote), shortPath(info.Point))
			}
		}
	}

	cmd := sshutil.BuildSSH(alias)
	supported, _ := c.askPassSupport()
	if warning := c.configureAskPass(cmd, target, supported); warning != "" {
		fmt.Fprintln(stderr, "警告:", warning)
	}
	cmd.Stdin = c.stdin
	cmd.Stdout = c.stdout
	cmd.Stderr = c.stderr
	fmt.Fprintf(stdout, "连接 %s（%s@%s:%d）...\n", alias, resolved.User, resolved.HostName, resolved.Port)
	sshErr := cmd.Run()

	code := 0
	if sshErr != nil {
		var exitErr *exec.ExitError
		if errors.As(sshErr, &exitErr) {
			code = exitErr.ExitCode()
			if code < 0 {
				code = 1
			}
		} else {
			fmt.Fprintln(stderr, "betterSSH: ssh 启动失败:", sshErr)
			code = 1
		}
	}

	if mountPoint != "" {
		if mounted, err := mount.IsMounted(mountPoint); err == nil && mounted {
			if *unmountAfter || (c.settings.AutoUnmount && didMount) {
				if err := c.unmountPoint(mountPoint); err != nil {
					fmt.Fprintln(stderr, "betterSSH: 自动卸载失败:", err)
					if code == 0 {
						code = 1
					}
				} else {
					fmt.Fprintf(stdout, "已卸载 %s\n", shortPath(mountPoint))
				}
			} else {
				fmt.Fprintf(stdout, "挂载仍保留: %s\n", shortPath(mountPoint))
				if *pointOverride != "" {
					fmt.Fprintf(stdout, "卸载: betterSSH unmount %s --mountpoint %q\n", alias, mountPoint)
				} else {
					fmt.Fprintf(stdout, "卸载: betterSSH unmount %s\n", alias)
				}
			}
		}
	}
	if code == 0 {
		fmt.Fprintln(stdout, "SSH 会话已结束")
	} else {
		fmt.Fprintf(stdout, "SSH 会话退出码: %d\n", code)
	}
	return code
}

func (c *cliContext) mountPointFor(alias, override string) (string, error) {
	raw := override
	if raw == "" {
		raw = c.settings.MountPoint(alias)
	}
	return mount.EnsureMountPoint(raw)
}

func (c *cliContext) remotePathFor(alias, override string) string {
	if override != "" {
		return override
	}
	return c.settings.RemotePathFor(alias)
}

func (c *cliContext) mountAlias(alias string, target sshutil.Target, remoteOverride, pointOverride string) (*cliMountInfo, error) {
	if _, err := sshutil.FindTool("sshfs"); err != nil {
		return nil, fmt.Errorf("无法挂载: %w", err)
	}
	if _, err := sshutil.FindTool("ssh"); err != nil {
		return nil, fmt.Errorf("无法挂载: %w", err)
	}
	for _, problem := range mount.CheckDependencies().Problems {
		fmt.Fprintln(c.stderr, "警告:", problem)
	}
	point, err := c.mountPointFor(alias, pointOverride)
	if err != nil {
		return nil, err
	}
	if mounted, err := mount.IsMounted(point); err != nil {
		return nil, fmt.Errorf("检查挂载状态: %w", err)
	} else if mounted {
		fmt.Fprintf(c.stdout, "挂载点已存在，复用: %s\n", shortPath(point))
		return &cliMountInfo{Point: point, DidMount: false}, nil
	}
	remote := c.remotePathFor(alias, remoteOverride)
	cmd, err := sshutil.BuildSSHFS(target, remote, point)
	if err != nil {
		return nil, err
	}
	supported, _ := c.askPassSupport()
	if warning := c.configureAskPass(cmd, target, supported); warning != "" {
		fmt.Fprintln(c.stderr, "警告:", warning)
	}
	cmd.Stdin = c.stdin
	cmd.Stdout = c.stdout
	cmd.Stderr = c.stderr
	fmt.Fprintf(c.stdout, "正在挂载 %s:%s -> %s\n", remoteTarget(target), remote, point)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sshfs 失败: %w", err)
	}
	mounted, err := mount.IsMounted(point)
	if err != nil {
		return nil, fmt.Errorf("检查挂载结果: %w", err)
	}
	if !mounted {
		return nil, errors.New("sshfs 退出成功但未检测到挂载点")
	}
	return &cliMountInfo{Point: point, DidMount: true}, nil
}

func (c *cliContext) unmountPoint(point string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return mount.Unmount(ctx, point)
}

func (c *cliContext) askPassSupport() (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	version, supported, _ := sshutil.AskPassSupport(ctx, "ssh")
	return supported, version
}

// configureAskPass only ever places the keyring key in the child environment.
func (c *cliContext) configureAskPass(cmd *exec.Cmd, target sshutil.Target, supported bool) string {
	cmd.Env = sshutil.WithoutAskPass(os.Environ())
	key := keyring.Key(target.User, target.HostName, target.Port)
	if _, err := passwordStore.Get(key); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ""
		}
		return "读取系统钥匙串失败，将回退到终端输入密码"
	}
	if !supported {
		return "已保存密码，但当前 OpenSSH 不支持 SSH_ASKPASS_REQUIRE=force；请手动输入密码"
	}
	if c.executable == "" {
		return "无法确定 betterSSH 可执行文件路径，请手动输入密码"
	}
	if err := sshutil.ApplyAskPass(cmd, c.executable, key); err != nil {
		return "AskPass 初始化失败，请手动输入密码"
	}
	return ""
}

func deleteStoredPassword(key string) (bool, error) {
	if _, err := passwordStore.Get(key); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if err := passwordStore.Delete(key); err != nil {
		return true, err
	}
	return true, nil
}

func remoteTarget(target sshutil.Target) string {
	if target.User == "" {
		return target.HostName
	}
	return target.User + "@" + target.HostName
}

func emptyDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}
