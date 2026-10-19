// Package directory holds the built-in user directory: username/password accounts and user
// groups that live in this deployment rather than on GitHub.
//
// GitHub OAuth answers "who is this" and the GitHub cache answers "which enterprise, organization
// and team are they in". A team without GitHub -- or without an enterprise administrator token --
// has neither, and so had no way to sign anybody in except the one local super administrator, and
// no way to grant anything except one login at a time. This package is that missing half:
//
//	data/directory.json
//	  users:   {username: {display_name, password_hash, password_salt, is_admin, disabled, ...}}
//	  groups:  {name: {description, members: [login, ...]}}
//
// Two properties shape everything else:
//
//  1. **One login namespace.** A built-in user's login is its username, and sessions, API keys,
//     traces and policy bindings all key on the login. So a built-in user and a GitHub account of
//     the same name would silently share all of that. The server refuses the collision from both
//     sides -- creating a built-in user named like a known GitHub login, and a GitHub sign-in whose
//     login is a built-in user -- and policy evaluation never asks GitHub about a built-in user.
//
//  2. **A group may hold GitHub logins too.** Membership is a list of logins, whichever door they
//     come through. That makes a group the same kind of scope as a GitHub team for every policy,
//     and it gives a deployment without an enterprise token a way to grant to a set of GitHub
//     users at once.
//
// Passwords are hashed exactly like the local super administrator's (salted scrypt, through
// internal/localadmin), never with the unsalted sha256 that is right for random API keys.
//
// No database: one small JSON document, re-read when its mtime changes, so several workers
// sharing data/ see each other's edits.
package directory

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/satomic/model-router/server-go/internal/jsonfile"
	"github.com/satomic/model-router/server-go/internal/localadmin"
)

// User is one built-in account as stored. The hash and salt never leave this package: every
// outward view goes through Public.
type User struct {
	Username     string `json:"username"`
	DisplayName  string `json:"display_name,omitempty"`
	Email        string `json:"email,omitempty"`
	PasswordHash string `json:"password_hash"`
	PasswordSalt string `json:"password_salt"`
	IsAdmin      bool   `json:"is_admin"`
	Disabled     bool   `json:"disabled"`
	MustChange   bool   `json:"must_change_password"`
	// CredentialVersion is copied into every session this user opens and compared on each
	// request. Bumping it -- a password change or reset -- signs out every other session at once,
	// without the session table having to be walked.
	CredentialVersion int     `json:"credential_version"`
	CreatedAt         float64 `json:"created_at"`
	UpdatedAt         float64 `json:"updated_at"`
	PasswordChangedAt float64 `json:"password_changed_at,omitempty"`
}

// Group is a named set of logins. Members may be built-in users or GitHub logins.
type Group struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Members     []string `json:"members"`
	CreatedAt   float64  `json:"created_at"`
	UpdatedAt   float64  `json:"updated_at"`
}

type document struct {
	Users  map[string]*User  `json:"users"`
	Groups map[string]*Group `json:"groups"`
}

// Errors the HTTP layer maps onto status codes.
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// ValidationError is a 422: the input is understood and wrong.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

var (
	// Usernames are deliberately GitHub-shaped plus '.' and '_': the login ends up in URLs,
	// trace file names and config.yaml keys, and nothing there wants spaces or slashes.
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// A group name becomes a config.yaml key (model_policy.user_groups) and a list entry. No
	// slash, so it can never be mistaken for a '<enterprise>/<team id>' team key.
	groupPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// A member is any login: a built-in username or a GitHub login.
	memberPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
)

func ValidateUsername(name string) error {
	if !usernamePattern.MatchString(name) {
		return invalid("username must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	return nil
}

func ValidateGroupName(name string) error {
	if !groupPattern.MatchString(name) {
		return invalid("group name must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	return nil
}

// ValidatePassword applies the local administrator's rules, so there is one password policy in
// the product rather than two.
func ValidatePassword(password string) error {
	if problem := localadmin.ValidateNewPassword(password); problem != "" {
		return invalid("%s", problem)
	}
	return nil
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func key(login string) string { return strings.ToLower(strings.TrimSpace(login)) }

// Store is the directory on disk plus an mtime-checked cache of it.
type Store struct {
	Path string

	mu    sync.Mutex
	doc   *document
	mtime float64
}

func New(dataDir string) *Store {
	s := &Store{Path: filepath.Join(dataDir, "directory.json")}
	s.mu.Lock()
	s.reload()
	s.mu.Unlock()
	return s
}

// reload re-reads the file when another writer has changed it. Callers hold s.mu.
func (s *Store) reload() {
	stamp := jsonfile.MTime(s.Path)
	if s.doc != nil && stamp == s.mtime {
		return
	}
	doc := &document{}
	if data, err := os.ReadFile(s.Path); err == nil {
		// A corrupt file must not block startup; it reads as an empty directory, the same rule
		// every other document under data/ follows.
		_ = json.Unmarshal(data, doc)
	}
	if doc.Users == nil {
		doc.Users = map[string]*User{}
	}
	if doc.Groups == nil {
		doc.Groups = map[string]*Group{}
	}
	s.doc = doc
	s.mtime = stamp
}

func (s *Store) save() error {
	if err := jsonfile.Write(s.Path, s.doc); err != nil {
		return err
	}
	// The file holds password hashes, so it is not world-readable.
	_ = os.Chmod(s.Path, 0o600)
	s.mtime = jsonfile.MTime(s.Path)
	return nil
}

// -- Package-level access ------------------------------------------------------
//
// Policy evaluation (internal/keypolicy, modelpolicy, scopepolicy) and the session check in
// internal/auth need membership answers, and they are reached from many call sites that only
// carry the configuration. One process-wide store, set at startup, saves threading it through all
// of them -- the same arrangement internal/ghcache uses for the GitHub side.

var (
	activeMu sync.RWMutex
	active   *Store
)

// Use makes `s` the store the package-level helpers answer from. nil switches them off.
func Use(s *Store) {
	activeMu.Lock()
	defer activeMu.Unlock()
	active = s
}

// Active returns the store set by Use, or nil.
func Active() *Store {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return active
}

// IsUser reports whether `login` is a built-in user (enabled or not).
func IsUser(login string) bool {
	if s := Active(); s != nil {
		return s.GetUser(login) != nil
	}
	return false
}

// LookupUser returns a copy of the built-in user, or nil.
func LookupUser(login string) *User {
	if s := Active(); s != nil {
		return s.GetUser(login)
	}
	return nil
}

// GroupsOf returns the names of the groups `login` belongs to, sorted.
func GroupsOf(login string) []string {
	if s := Active(); s != nil {
		return s.GroupsOf(login)
	}
	return nil
}

// HasEnabledUsers reports whether any built-in account can sign in at all, which is what decides
// whether the console offers the username/password form.
func HasEnabledUsers() bool {
	if s := Active(); s != nil {
		return s.HasEnabledUsers()
	}
	return false
}

// -- Users ---------------------------------------------------------------------

func clone(u *User) *User {
	if u == nil {
		return nil
	}
	copied := *u
	return &copied
}

func (s *Store) GetUser(login string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	return clone(s.doc.Users[key(login)])
}

func (s *Store) HasEnabledUsers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	for _, user := range s.doc.Users {
		if !user.Disabled {
			return true
		}
	}
	return false
}

// ListUsers returns every user, alphabetically.
func (s *Store) ListUsers() []*User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	out := make([]*User, 0, len(s.doc.Users))
	for _, user := range s.doc.Users {
		out = append(out, clone(user))
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i].Username) < key(out[j].Username) })
	return out
}

// NewUser is what an administrator submits to create an account.
type NewUser struct {
	Username    string
	DisplayName string
	Email       string
	Password    string
	IsAdmin     bool
	MustChange  bool
}

// CreateUser adds an account. Name collisions with *other* identity sources (GitHub logins, the
// local administrator) are the caller's to check: this package does not know about them.
func (s *Store) CreateUser(input NewUser) (*User, error) {
	username := strings.TrimSpace(input.Username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(input.Password); err != nil {
		return nil, err
	}
	salt, digest, err := localadmin.HashPassword(input.Password, "")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	if s.doc.Users[key(username)] != nil {
		return nil, ErrExists
	}
	stamp := now()
	user := &User{
		Username:          username,
		DisplayName:       strings.TrimSpace(input.DisplayName),
		Email:             strings.TrimSpace(input.Email),
		PasswordHash:      digest,
		PasswordSalt:      salt,
		IsAdmin:           input.IsAdmin,
		MustChange:        input.MustChange,
		CredentialVersion: 1,
		CreatedAt:         stamp,
		UpdatedAt:         stamp,
		PasswordChangedAt: stamp,
	}
	s.doc.Users[key(username)] = user
	if err := s.save(); err != nil {
		delete(s.doc.Users, key(username))
		return nil, err
	}
	return clone(user), nil
}

// UserPatch carries the fields an administrator may change. A nil field is left alone.
type UserPatch struct {
	DisplayName *string
	Email       *string
	IsAdmin     *bool
	Disabled    *bool
	MustChange  *bool
	// Password resets the credential: a new hash, and every existing session signed out.
	Password *string
}

func (s *Store) UpdateUser(login string, patch UserPatch) (*User, error) {
	var salt, digest string
	if patch.Password != nil {
		if err := ValidatePassword(*patch.Password); err != nil {
			return nil, err
		}
		var err error
		salt, digest, err = localadmin.HashPassword(*patch.Password, "")
		if err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	user := s.doc.Users[key(login)]
	if user == nil {
		return nil, ErrNotFound
	}
	before := clone(user)
	if patch.DisplayName != nil {
		user.DisplayName = strings.TrimSpace(*patch.DisplayName)
	}
	if patch.Email != nil {
		user.Email = strings.TrimSpace(*patch.Email)
	}
	if patch.IsAdmin != nil {
		user.IsAdmin = *patch.IsAdmin
	}
	if patch.Disabled != nil {
		user.Disabled = *patch.Disabled
	}
	if patch.MustChange != nil {
		user.MustChange = *patch.MustChange
	}
	stamp := now()
	if patch.Password != nil {
		user.PasswordHash, user.PasswordSalt = digest, salt
		user.CredentialVersion++
		user.PasswordChangedAt = stamp
	}
	user.UpdatedAt = stamp
	if err := s.save(); err != nil {
		*user = *before
		return nil, err
	}
	return clone(user), nil
}

// DeleteUser removes the account and its group memberships.
func (s *Store) DeleteUser(login string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	k := key(login)
	if s.doc.Users[k] == nil {
		return ErrNotFound
	}
	delete(s.doc.Users, k)
	stamp := now()
	for _, group := range s.doc.Groups {
		kept := group.Members[:0:0]
		for _, member := range group.Members {
			if key(member) != k {
				kept = append(kept, member)
			}
		}
		if len(kept) != len(group.Members) {
			group.Members = kept
			group.UpdatedAt = stamp
		}
	}
	return s.save()
}

// Authenticate checks a username and password. It returns the user on success, and nil for a
// wrong password *or* an unknown username -- one answer for both, so the sign-in form cannot be
// used to discover which accounts exist. Disabled accounts authenticate (the caller decides what
// to say about them), so that a disabled user who knows their password learns why they cannot
// get in.
func (s *Store) Authenticate(username, password string) *User {
	user := s.GetUser(username)
	if user == nil {
		// Spend the same scrypt work as a real check, so timing does not reveal the miss.
		_, _, _ = localadmin.HashPassword(password, "00000000000000000000000000000000")
		return nil
	}
	_, candidate, err := localadmin.HashPassword(password, user.PasswordSalt)
	if err != nil {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(candidate), []byte(user.PasswordHash)) != 1 {
		return nil
	}
	return user
}

// ChangeOwnPassword is the self-service change: the current password is required, the new one is
// validated, and the credential version moves on so every *other* session ends.
func (s *Store) ChangeOwnPassword(login, current, next string) (*User, error) {
	if s.Authenticate(login, current) == nil {
		return nil, invalid("the current password is incorrect")
	}
	if current == next {
		return nil, invalid("choose a password different from the current one")
	}
	mustChange := false
	return s.UpdateUser(login, UserPatch{Password: &next, MustChange: &mustChange})
}

// -- Groups --------------------------------------------------------------------

func cloneGroup(g *Group) *Group {
	if g == nil {
		return nil
	}
	copied := *g
	copied.Members = append([]string{}, g.Members...)
	return &copied
}

func (s *Store) GetGroup(name string) *Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	return cloneGroup(s.doc.Groups[key(name)])
}

func (s *Store) ListGroups() []*Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	out := make([]*Group, 0, len(s.doc.Groups))
	for _, group := range s.doc.Groups {
		out = append(out, cloneGroup(group))
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i].Name) < key(out[j].Name) })
	return out
}

// GroupsOf returns the names of every group `login` is a member of, sorted.
func (s *Store) GroupsOf(login string) []string {
	k := key(login)
	if k == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	out := []string{}
	for _, group := range s.doc.Groups {
		for _, member := range group.Members {
			if key(member) == k {
				out = append(out, group.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// normalizeMembers trims, validates and de-duplicates (case-insensitively), keeping first-seen
// spelling and order.
func normalizeMembers(members []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, member := range members {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		if !memberPattern.MatchString(member) {
			return nil, invalid("%q is not a valid login", member)
		}
		if seen[key(member)] {
			continue
		}
		seen[key(member)] = true
		out = append(out, member)
	}
	return out, nil
}

func (s *Store) CreateGroup(name, description string, members []string) (*Group, error) {
	name = strings.TrimSpace(name)
	if err := ValidateGroupName(name); err != nil {
		return nil, err
	}
	cleaned, err := normalizeMembers(members)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	if s.doc.Groups[key(name)] != nil {
		return nil, ErrExists
	}
	stamp := now()
	group := &Group{
		Name:        name,
		Description: strings.TrimSpace(description),
		Members:     cleaned,
		CreatedAt:   stamp,
		UpdatedAt:   stamp,
	}
	s.doc.Groups[key(name)] = group
	if err := s.save(); err != nil {
		delete(s.doc.Groups, key(name))
		return nil, err
	}
	return cloneGroup(group), nil
}

// UpdateGroup replaces the description and/or the member list. nil leaves a field alone.
func (s *Store) UpdateGroup(name string, description *string, members []string) (*Group, error) {
	var cleaned []string
	if members != nil {
		var err error
		if cleaned, err = normalizeMembers(members); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	group := s.doc.Groups[key(name)]
	if group == nil {
		return nil, ErrNotFound
	}
	before := cloneGroup(group)
	if description != nil {
		group.Description = strings.TrimSpace(*description)
	}
	if members != nil {
		group.Members = cleaned
	}
	group.UpdatedAt = now()
	if err := s.save(); err != nil {
		*group = *before
		return nil, err
	}
	return cloneGroup(group), nil
}

func (s *Store) DeleteGroup(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	if s.doc.Groups[key(name)] == nil {
		return ErrNotFound
	}
	delete(s.doc.Groups, key(name))
	return s.save()
}

// SetUserGroups puts `login` into exactly the named groups (and out of every other one), in one
// write. The user editor works per user, and doing this as N group updates would be N writes that
// another worker could interleave with.
func (s *Store) SetUserGroups(login string, names []string) error {
	k := key(login)
	want := map[string]bool{}
	for _, name := range names {
		want[key(name)] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	for name := range want {
		if s.doc.Groups[name] == nil {
			return invalid("unknown group %q", name)
		}
	}
	stamp := now()
	for groupKey, group := range s.doc.Groups {
		has := false
		kept := group.Members[:0:0]
		for _, member := range group.Members {
			if key(member) == k {
				has = true
				continue
			}
			kept = append(kept, member)
		}
		switch {
		case want[groupKey] && !has:
			group.Members = append(group.Members, login)
			group.UpdatedAt = stamp
		case !want[groupKey] && has:
			group.Members = kept
			group.UpdatedAt = stamp
		}
	}
	return s.save()
}
