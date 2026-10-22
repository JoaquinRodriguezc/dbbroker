package broker

import "testing"

func TestRedisURLCarriesWorktreeCredentials(t *testing.T) {
	got := redisURL("redis://127.0.0.1:56379/0", "dbb_wt_abc", "s3cret")
	if got != "redis://dbb_wt_abc:s3cret@127.0.0.1:56379/0" {
		t.Fatalf("unexpected url: %s", got)
	}
}

func TestRedisKeyPrefixMatchesUser(t *testing.T) {
	if got := redisKeyPrefix(worktreeDB("repo", "/tmp/a")); got != worktreeDB("repo", "/tmp/a")+":" {
		t.Fatalf("unexpected prefix: %s", got)
	}
}

func TestRedisPasswordsAreUnique(t *testing.T) {
	a, err := newRedisPassword()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newRedisPassword()
	if len(a) != 48 || a == b {
		t.Fatalf("weak password generation: %q %q", a, b)
	}
}
