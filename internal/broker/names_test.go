package broker

import "testing"

func TestStableWorktreeName(t *testing.T) {
	a := worktreeDB("repo", "/tmp/a")
	if a != worktreeDB("repo", "/tmp/a") {
		t.Fatal("name must be stable")
	}
	if a == worktreeDB("repo", "/tmp/b") {
		t.Fatal("different worktrees must differ")
	}
}

func TestTemplateNameIsSafe(t *testing.T) {
	if got := templateDB("repo", "Release 42!!", "abc"); got[:19] != "dbb_tpl_release_42_" {
		t.Fatalf("unexpected name: %s", got)
	}
}

func TestTemplateNamesAreScopedByRepository(t *testing.T) {
	a := templateDB("repo-a", "main", "same-backup")
	b := templateDB("repo-b", "main", "same-backup")
	if a == b {
		t.Fatal("same template name in different repositories must differ")
	}
}
