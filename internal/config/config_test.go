package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = `# top comment
Host alpha beta   # alpha comment
    # keep this comment
    HostName alpha.example   # inline comment
    User alice
    Port 2222
    IdentityFile "~/.ssh/id alpha"
    ProxyCommand ssh -W %h:%p jump
    WeirdOption keep me

# gap comment
Host gamma
    HostName gamma.example
    User bob

Host *
    User wildcard
    ServerAliveInterval 30
`

func TestParsePreservesStructure(t *testing.T) {
	f, err := Parse("config", []byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(f.Render()); got != sampleConfig {
		t.Fatalf("render changed file:\n--- got ---\n%s\n--- want ---\n%s", got, sampleConfig)
	}
	if len(f.Hosts) != 3 {
		t.Fatalf("got %d hosts, want 3", len(f.Hosts))
	}
	if got := f.Hosts[0].PrimaryAlias(); got != "alpha" {
		t.Fatalf("primary alias = %q", got)
	}
	if !strings.Contains(f.Hosts[0].Comment, "alpha comment") {
		t.Fatalf("host comment = %q", f.Hosts[0].Comment)
	}
	if got := f.Hosts[0].Field("HostName"); got != "alpha.example" {
		t.Fatalf("hostname = %q", got)
	}
	if got := f.Hosts[0].Field("IdentityFile"); got != "~/.ssh/id alpha" {
		t.Fatalf("unquoted identity = %q", got)
	}
	if entries := f.HostEntries(); len(entries) != 2 {
		t.Fatalf("concrete entries = %d, want 2", len(entries))
	}
}

func TestUpdateHostKeepsUnknownAndComments(t *testing.T) {
	f, err := Parse("config", []byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	in := InputFromHost(f.Hosts[0])
	in.HostName = "new.example"
	in.Port = ""
	in.ProxyJump = "jump.example"
	in.ServerAliveInterval = "15"
	if err := f.UpdateHost(0, in); err != nil {
		t.Fatal(err)
	}
	got := string(f.Render())
	for _, want := range []string{
		"# top comment",
		"# keep this comment",
		"HostName new.example # inline comment",
		"ProxyCommand ssh -W %h:%p jump",
		"WeirdOption keep me",
		"# gap comment",
		"Host gamma",
		"Host *",
		"User wildcard",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("updated config lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n    Port ") {
		t.Errorf("Port should have been removed:\n%s", got)
	}
	if !strings.Contains(got, "    ProxyJump jump.example") {
		t.Errorf("ProxyJump not inserted:\n%s", got)
	}
	if !strings.Contains(got, "    ServerAliveInterval 15") {
		t.Errorf("ServerAliveInterval not inserted:\n%s", got)
	}
}

func TestAddAndDeleteHost(t *testing.T) {
	f, err := Parse("config", []byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	err = f.AddHost(HostInput{
		Aliases:      []string{"delta"},
		HostName:     "delta.example",
		User:         "dana",
		Port:         "2200",
		IdentityFile: "~/.ssh/delta",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.Render()), "Host delta") {
		t.Fatal("new host missing")
	}
	if err := f.AddHost(HostInput{Aliases: []string{"alpha"}}); err == nil {
		t.Fatal("expected duplicate alias error")
	}
	if err := f.DeleteHost(0); err != nil {
		t.Fatal(err)
	}
	got := string(f.Render())
	if strings.Contains(got, "Host alpha") {
		t.Fatal("deleted host still present")
	}
	for _, want := range []string{"# top comment", "# gap comment", "Host gamma", "Host *"} {
		if !strings.Contains(got, want) {
			t.Errorf("delete lost %q:\n%s", want, got)
		}
	}
}

func TestSaveCreatesBackupAndPreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := strings.ReplaceAll(sampleConfig, "\n", "\r\n")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	in := InputFromHost(f.Hosts[0])
	in.User = "carol"
	if err := f.UpdateHost(0, in); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, []byte(original)) {
		t.Fatal("backup does not match original")
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("User carol\r\n")) {
		t.Fatalf("CRLF not preserved: %q", updated)
	}
	if bytes.Contains(updated, []byte("\n")) && !bytes.Contains(updated, []byte("\r\n")) {
		t.Fatal("line endings were normalized")
	}
}

func TestSaveRejectsExternalModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	in := InputFromHost(f.Hosts[0])
	in.User = "changed"
	if err := f.UpdateHost(0, in); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# external\n"+sampleConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); !errors.Is(err, ErrModified) {
		t.Fatalf("Save error = %v, want ErrModified", err)
	}
	current, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(current), "# external\n") {
		t.Fatal("externally modified file was overwritten")
	}
}

func TestResolveInheritedValues(t *testing.T) {
	f, err := Parse("config", []byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Resolve(1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Alias != "gamma" || r.HostName != "gamma.example" {
		t.Fatalf("resolved = %+v", r)
	}
	if r.User != "bob" {
		t.Fatalf("direct user = %q, want bob", r.User)
	}
	if r.Port != DefaultPort {
		t.Fatalf("port = %d", r.Port)
	}
	if r.ServerAliveInterval != "30" {
		t.Fatalf("server alive = %q", r.ServerAliveInterval)
	}
}

func TestParseNoFinalNewlineAndMissingFile(t *testing.T) {
	f, err := Parse("config", []byte("Host a\n  HostName a.example"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(f.Render()); got != "Host a\n  HostName a.example" {
		t.Fatalf("final newline changed: %q", got)
	}
	missing := filepath.Join(t.TempDir(), "nested", "config")
	nf, err := Load(missing)
	if err != nil {
		t.Fatal(err)
	}
	if nf.exists {
		t.Fatal("missing file reported as existing")
	}
	if err := nf.AddHost(HostInput{Aliases: []string{"new"}, HostName: "new.example"}); err != nil {
		t.Fatal(err)
	}
	if err := nf.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(missing)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Host new") {
		t.Fatalf("new config = %q", data)
	}
}

func TestValidateInput(t *testing.T) {
	cases := []HostInput{
		{},
		{Aliases: []string{"*"}},
		{Aliases: []string{"ok"}, Port: "99999"},
		{Aliases: []string{"ok"}, User: "a b"},
		{Aliases: []string{"ok"}, ServerAliveInterval: "-1"},
		{Aliases: []string{"-oProxyCommand=bad"}},
	}
	for i, in := range cases {
		if err := ValidateInput(&in); err == nil {
			t.Errorf("case %d should fail", i)
		}
	}
	in := HostInput{Aliases: []string{"ok", "ok"}}
	if err := ValidateInput(&in); err == nil {
		t.Fatal("duplicate aliases should fail")
	}
	in = HostInput{Aliases: []string{"ok"}, Port: "22"}
	if err := ValidateInput(&in); err != nil {
		t.Fatal(err)
	}
	in = HostInput{Aliases: []string{"ok", "!blocked", "*.example"}}
	if err := ValidateInput(&in); err != nil {
		t.Fatalf("mixed patterns should be preserved: %v", err)
	}
	if !strings.Contains(strings.Join(in.Aliases, " "), "!blocked") {
		t.Fatalf("negated pattern was dropped: %v", in.Aliases)
	}
}

func TestResolveWildcardDefault(t *testing.T) {
	f, err := Parse("config", []byte("Host *\n  User wildcard\n  ServerAliveInterval 30\n\nHost delta\n  HostName delta.example\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Resolve(1)
	if err != nil {
		t.Fatal(err)
	}
	if r.User != "wildcard" || r.ServerAliveInterval != "30" {
		t.Fatalf("resolved defaults = %+v", r)
	}
}

func TestSingleEmptyLineRoundTrip(t *testing.T) {
	f, err := Parse("config", []byte("\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(f.Render()); got != "\n" {
		t.Fatalf("single empty line rendered as %q", got)
	}
}
