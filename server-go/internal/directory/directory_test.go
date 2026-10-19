package directory

import (
	"os"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

func TestCreateAuthenticateAndReset(t *testing.T) {
	s := newStore(t)
	user, err := s.CreateUser(NewUser{Username: "Alice", Password: "correct horse", MustChange: true})
	if err != nil {
		t.Fatal(err)
	}
	if user.PasswordHash == "" || user.PasswordHash == "correct horse" {
		t.Fatal("the password must be stored hashed")
	}
	if s.Authenticate("alice", "correct horse") == nil {
		t.Error("lookup should be case-insensitive and the right password should pass")
	}
	if s.Authenticate("alice", "wrong password") != nil {
		t.Error("a wrong password passed")
	}
	if s.Authenticate("nobody", "correct horse") != nil {
		t.Error("an unknown user passed")
	}
	if _, err := s.CreateUser(NewUser{Username: "ALICE", Password: "another one"}); err != ErrExists {
		t.Errorf("a case-variant duplicate should be refused, got %v", err)
	}

	next := "battery staple"
	updated, err := s.UpdateUser("alice", UserPatch{Password: &next})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CredentialVersion != user.CredentialVersion+1 {
		t.Error("a password reset must move the credential version on, or old sessions survive it")
	}
	if s.Authenticate("alice", "correct horse") != nil || s.Authenticate("alice", next) == nil {
		t.Error("the reset did not replace the password")
	}

	if _, err := s.ChangeOwnPassword("alice", "wrong", "whatever123"); err == nil {
		t.Error("a self-service change must require the current password")
	}
	changed, err := s.ChangeOwnPassword("alice", next, "a fresh password")
	if err != nil {
		t.Fatal(err)
	}
	if changed.MustChange {
		t.Error("changing the password must clear the forced-change flag")
	}
}

func TestValidation(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"", "has space", "a/b", "-leading", "x@y"} {
		if _, err := s.CreateUser(NewUser{Username: bad, Password: "long enough"}); err == nil {
			t.Errorf("username %q should be refused", bad)
		}
	}
	if _, err := s.CreateUser(NewUser{Username: "bob", Password: "short"}); err == nil {
		t.Error("a short password should be refused")
	}
	if _, err := s.CreateGroup("a/b", "", nil); err == nil {
		t.Error("a group name with a slash would read as a team key and must be refused")
	}
}

func TestGroupsAndMembership(t *testing.T) {
	s := newStore(t)
	if _, err := s.CreateUser(NewUser{Username: "carol", Password: "long enough"}); err != nil {
		t.Fatal(err)
	}
	// A group may hold built-in users and GitHub logins alike; duplicates collapse.
	if _, err := s.CreateGroup("research", "the lab", []string{"carol", "octocat", "Carol"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateGroup("ops", "", nil); err != nil {
		t.Fatal(err)
	}
	if got := s.GetGroup("research").Members; len(got) != 2 {
		t.Errorf("members = %v, want carol and octocat once each", got)
	}
	if got := s.GroupsOf("OCTOCAT"); len(got) != 1 || got[0] != "research" {
		t.Errorf("GroupsOf(octocat) = %v", got)
	}

	if err := s.SetUserGroups("carol", []string{"ops"}); err != nil {
		t.Fatal(err)
	}
	if got := s.GroupsOf("carol"); len(got) != 1 || got[0] != "ops" {
		t.Errorf("after SetUserGroups carol is in %v, want only ops", got)
	}
	if err := s.SetUserGroups("carol", []string{"missing"}); err == nil {
		t.Error("an unknown group should be refused")
	}

	if err := s.DeleteUser("carol"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetGroup("ops").Members; len(got) != 0 {
		t.Errorf("deleting a user must remove them from their groups, ops still has %v", got)
	}
}

// Another worker's write must be seen: the store re-reads the file when it changes.
func TestSharedFileIsReread(t *testing.T) {
	dir := t.TempDir()
	first, second := New(dir), New(dir)
	if _, err := first.CreateUser(NewUser{Username: "dave", Password: "long enough"}); err != nil {
		t.Fatal(err)
	}
	if second.GetUser("dave") == nil {
		t.Error("a second store over the same directory did not see the new user")
	}
	info, err := os.Stat(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("directory.json holds password hashes and is mode %v", info.Mode().Perm())
	}
}
