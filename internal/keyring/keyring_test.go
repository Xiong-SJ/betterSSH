package keyring

import "testing"

func TestKey(t *testing.T) {
	cases := []struct {
		user, host string
		port       int
		want       string
	}{
		{"alice", "example.com", 22, "alice@example.com:22"},
		{"root", "192.0.2.10", 2222, "root@192.0.2.10:2222"},
		{"", "host", 0, "@host:22"},
		{" alice ", " host ", 22, "alice@host:22"},
	}
	for _, tc := range cases {
		if got := Key(tc.user, tc.host, tc.port); got != tc.want {
			t.Errorf("Key(%q, %q, %d) = %q, want %q", tc.user, tc.host, tc.port, got, tc.want)
		}
	}
}
