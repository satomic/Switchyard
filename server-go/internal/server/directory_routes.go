package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/satomic/model-router/server-go/internal/auth"
	"github.com/satomic/model-router/server-go/internal/config"
	"github.com/satomic/model-router/server-go/internal/directory"
	"github.com/satomic/model-router/server-go/internal/modelpolicy"
	"github.com/satomic/model-router/server-go/internal/omap"
)

// The user directory: built-in accounts and user groups (administrators only).
//
//	GET    /v1/directory                     users, groups, and the GitHub logins a group may take
//	POST   /v1/directory/users               create a built-in user
//	PATCH  /v1/directory/users/{username}    edit, disable, reset the password, set its groups
//	DELETE /v1/directory/users/{username}    delete, along with its API keys and sessions
//	POST   /v1/directory/groups              create a group
//	PATCH  /v1/directory/groups/{name}       edit the description or the member list
//	DELETE /v1/directory/groups/{name}       delete, and drop it from every policy
//
// The directory is not part of config.yaml, so these write data/directory.json directly instead
// of going through PUT /v1/config. The one exception is deleting a group, which also removes the
// group's name from the three policies that may reference it.

// directoryError maps the directory package's errors onto HTTP statuses.
func directoryError(err error) error {
	var invalid *directory.ValidationError
	switch {
	case errors.As(err, &invalid):
		return errorf(http.StatusUnprocessableEntity, "%s", invalid.Message)
	case errors.Is(err, directory.ErrNotFound):
		return errorf(http.StatusNotFound, "not found")
	case errors.Is(err, directory.ErrExists):
		return errorf(http.StatusConflict, "that name is already taken")
	}
	return errorf(http.StatusInternalServerError, "%v", err)
}

func (a *App) directoryStore() (*directory.Store, error) {
	store := directory.Active()
	if store == nil {
		return nil, errorf(http.StatusServiceUnavailable, "the user directory is not available")
	}
	return store, nil
}

// publicUser is the outward view of an account: never the hash or the salt.
func publicUser(user *directory.User, groups []string, known *omap.Map, keyCount int) *omap.Map {
	out := mapOf(
		"username", user.Username,
		"display_name", user.DisplayName,
		"email", user.Email,
		"is_admin", user.IsAdmin,
		"disabled", user.Disabled,
		"must_change_password", user.MustChange,
		"created_at", omap.Num(user.CreatedAt),
		"updated_at", omap.Num(user.UpdatedAt),
		"password_changed_at", omap.Num(user.PasswordChangedAt),
		"groups", stringsAny(groups),
		"key_count", json.Number(strconv.Itoa(keyCount)),
		"last_seen", nil,
		"sign_ins", json.Number("0"),
	)
	if known != nil {
		out.Set("last_seen", known.Value("last_seen"))
		out.Set("sign_ins", known.Value("sign_ins"))
	}
	return out
}

func stringsAny(items []string) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}

// groupReferences reports where a group is used in the saved policies, so the console can show it
// next to the group and warn before a delete.
func groupReferences(cfg *config.RouterConfig, name string) *omap.Map {
	modelGroup := ""
	if table := cfg.ModelPolicy.Map("user_groups"); table != nil {
		for _, key := range table.Keys() {
			if strings.EqualFold(key, name) {
				modelGroup = omap.AsString(table.Value(key))
			}
		}
	}
	listed := func(policy *omap.Map) bool {
		for _, item := range omap.StringSlice(policy.Value("user_groups")) {
			if strings.EqualFold(strings.TrimSpace(item), name) {
				return true
			}
		}
		return false
	}
	return mapOf(
		"model_group", modelGroup,
		"key_policy", listed(cfg.KeyPolicy),
		"key_scope", listed(cfg.KeyScopePolicy),
	)
}

// getDirectory returns the whole directory in one response: the page shows users and groups side
// by side, and a group's member picker needs both the built-in users and the GitHub logins.
func (a *App) getDirectory(w http.ResponseWriter, r *http.Request) error {
	if _, err := a.admin(r); err != nil {
		return err
	}
	store, err := a.directoryStore()
	if err != nil {
		return err
	}
	cfg := a.Config()

	known := omap.New()
	github := []any{}
	for _, entry := range a.AuthStore.ListKnownUsers() {
		known.Set(strings.ToLower(entry.Str("login")), entry)
		if kind := entry.Str("kind"); kind == "" || kind == "github" {
			github = append(github, mapOf(
				"login", entry.Value("login"),
				"name", entry.Value("name"),
				"avatar_url", entry.Value("avatar_url"),
				"last_seen", entry.Value("last_seen"),
			))
		}
	}
	keyCounts := map[string]int{}
	for _, key := range a.AuthStore.ListAPIKeys("", false) {
		keyCounts[strings.ToLower(key.Str("user_login"))]++
	}

	users := []any{}
	for _, user := range store.ListUsers() {
		lowered := strings.ToLower(user.Username)
		users = append(users, publicUser(user, store.GroupsOf(user.Username), known.Map(lowered), keyCounts[lowered]))
	}
	groups := []any{}
	for _, group := range store.ListGroups() {
		members := []any{}
		for _, member := range group.Members {
			kind := "github"
			name := member
			if record := store.GetUser(member); record != nil {
				kind = "builtin"
				if record.DisplayName != "" {
					name = record.DisplayName
				}
			} else if entry := known.Map(strings.ToLower(member)); entry != nil && entry.Str("name") != "" {
				name = entry.Str("name")
			}
			members = append(members, mapOf("login", member, "kind", kind, "name", name))
		}
		groups = append(groups, mapOf(
			"name", group.Name,
			"description", group.Description,
			"members", members,
			"created_at", omap.Num(group.CreatedAt),
			"updated_at", omap.Num(group.UpdatedAt),
			"references", groupReferences(cfg, group.Name),
		))
	}

	localAdmin := ""
	if cfg.LocalAdminEnabled() {
		localAdmin = cfg.LocalAdminUsername()
	}
	writeJSON(w, http.StatusOK, mapOf(
		"users", users,
		"groups", groups,
		"github_users", github,
		"oauth_configured", cfg.OAuthConfigured(),
		"local_admin_username", localAdmin,
	), nil)
	return nil
}

// usernameConflict explains why `username` cannot become a built-in user, or returns "".
//
// The login namespace is shared: sessions, keys, traces and policy bindings all key on it. So a
// name that already means somebody -- the local administrator, a configured GitHub administrator,
// a GitHub account that has signed in -- is refused rather than merged.
func (a *App) usernameConflict(username string) string {
	cfg := a.Config()
	if strings.EqualFold(username, cfg.LocalAdminUsername()) {
		return username + " is the local administrator's username"
	}
	if cfg.IsAdminLogin(username) {
		return username + " is listed in auth.admin_logins as a GitHub administrator"
	}
	for _, entry := range a.AuthStore.ListKnownUsers() {
		if strings.EqualFold(entry.Str("login"), username) {
			if kind := entry.Str("kind"); kind == "" || kind == "github" {
				return username + " is a GitHub account that has already signed in to this service"
			}
		}
	}
	return ""
}

func optionalString(payload *omap.Map, field string) *string {
	if !payload.Has(field) {
		return nil
	}
	value := omap.AsString(payload.Value(field))
	return &value
}

func optionalBool(payload *omap.Map, field string) (*bool, error) {
	if !payload.Has(field) {
		return nil, nil
	}
	value, ok := payload.Value(field).(bool)
	if !ok {
		return nil, errorf(http.StatusUnprocessableEntity, "%s must be a boolean", field)
	}
	return &value, nil
}

// optionalList reads a list of names; nil when the field is absent, so "not sent" and "empty"
// stay different answers.
func optionalList(payload *omap.Map, field string) ([]string, error) {
	if !payload.Has(field) {
		return nil, nil
	}
	raw, ok := payload.Value(field).([]any)
	if !ok && payload.Value(field) != nil {
		return nil, errorf(http.StatusUnprocessableEntity, "%s must be a list", field)
	}
	out := []string{}
	for _, item := range raw {
		out = append(out, omap.AsString(item))
	}
	return out, nil
}

// directoryChanged drops what was computed from the old directory: memoised model sets.
func directoryChanged() { modelpolicy.Invalidate() }

func (a *App) createDirectoryUser(w http.ResponseWriter, r *http.Request) error {
	admin, err := a.admin(r)
	if err != nil {
		return err
	}
	store, err := a.directoryStore()
	if err != nil {
		return err
	}
	payload, err := readJSONObject(r)
	if err != nil {
		return err
	}
	username := strings.TrimSpace(payload.Str("username"))
	if err := directory.ValidateUsername(username); err != nil {
		return directoryError(err)
	}
	if conflict := a.usernameConflict(username); conflict != "" {
		return errorf(http.StatusConflict, "%s; choose another username", conflict)
	}
	groups, err := optionalList(payload, "groups")
	if err != nil {
		return err
	}
	user, err := store.CreateUser(directory.NewUser{
		Username:    username,
		DisplayName: payload.Str("display_name"),
		Email:       payload.Str("email"),
		Password:    payload.Str("password"),
		IsAdmin:     payload.Bool("is_admin", false),
		// On by default: an administrator who typed the first password knows it, and the user
		// should not keep a credential somebody else chose.
		MustChange: payload.Bool("must_change_password", true),
	})
	if err != nil {
		return directoryError(err)
	}
	if len(groups) > 0 {
		if err := store.SetUserGroups(user.Username, groups); err != nil {
			_ = store.DeleteUser(user.Username)
			return directoryError(err)
		}
	}
	directoryChanged()
	logInfo("built-in user created by=%s username=%s admin=%v", admin.Str("login"), user.Username, user.IsAdmin)
	writeJSON(w, http.StatusCreated, publicUser(user, store.GroupsOf(user.Username), nil, 0), nil)
	return nil
}

// isSelf reports whether the session belongs to the built-in user `username`.
func isSelf(session *omap.Map, username string) bool {
	return session.Bool("local_user", false) && strings.EqualFold(session.Str("login"), username)
}

func (a *App) updateDirectoryUser(w http.ResponseWriter, r *http.Request) error {
	admin, err := a.admin(r)
	if err != nil {
		return err
	}
	store, err := a.directoryStore()
	if err != nil {
		return err
	}
	username := r.PathValue("username")
	if store.GetUser(username) == nil {
		return errorf(http.StatusNotFound, "no such user")
	}
	payload, err := readJSONObject(r)
	if err != nil {
		return err
	}
	patch := directory.UserPatch{
		DisplayName: optionalString(payload, "display_name"),
		Email:       optionalString(payload, "email"),
	}
	if patch.IsAdmin, err = optionalBool(payload, "is_admin"); err != nil {
		return err
	}
	if patch.Disabled, err = optionalBool(payload, "disabled"); err != nil {
		return err
	}
	if patch.MustChange, err = optionalBool(payload, "must_change_password"); err != nil {
		return err
	}
	if password := optionalString(payload, "password"); password != nil && *password != "" {
		patch.Password = password
	}
	groups, err := optionalList(payload, "groups")
	if err != nil {
		return err
	}
	// An administrator cannot lock themselves out from their own console: the only other way back
	// in would be editing data/directory.json by hand.
	if isSelf(admin, username) {
		if patch.IsAdmin != nil && !*patch.IsAdmin {
			return errorf(http.StatusConflict, "you cannot remove your own administrator role")
		}
		if patch.Disabled != nil && *patch.Disabled {
			return errorf(http.StatusConflict, "you cannot disable your own account")
		}
	}

	user, err := store.UpdateUser(username, patch)
	if err != nil {
		return directoryError(err)
	}
	if groups != nil {
		if err := store.SetUserGroups(user.Username, groups); err != nil {
			return directoryError(err)
		}
	}
	if patch.Password != nil && isSelf(admin, username) {
		// Resetting your own password from the admin page keeps you signed in, as changing it
		// through the self-service form does.
		a.AuthStore.UpdateSession(auth.Cookie(r, auth.SessionCookie), mapOf(
			"credential_version", json.Number(strconv.Itoa(user.CredentialVersion)),
		))
	}
	directoryChanged()
	fields := []string{}
	for _, field := range payload.Keys() {
		if field == "password" {
			field = "password(reset)"
		}
		fields = append(fields, field)
	}
	logInfo("built-in user updated by=%s username=%s fields=%s", admin.Str("login"), user.Username, strings.Join(fields, ","))
	writeJSON(w, http.StatusOK, publicUser(user, store.GroupsOf(user.Username), nil, len(a.AuthStore.ListAPIKeys(user.Username, false))), nil)
	return nil
}

func (a *App) deleteDirectoryUser(w http.ResponseWriter, r *http.Request) error {
	admin, err := a.admin(r)
	if err != nil {
		return err
	}
	store, err := a.directoryStore()
	if err != nil {
		return err
	}
	username := r.PathValue("username")
	user := store.GetUser(username)
	if user == nil {
		return errorf(http.StatusNotFound, "no such user")
	}
	if isSelf(admin, username) {
		return errorf(http.StatusConflict, "you cannot delete your own account")
	}
	if err := store.DeleteUser(username); err != nil {
		return directoryError(err)
	}
	// Everything keyed on the login goes with the account, so a later account that reuses the
	// name starts clean. Traces stay: they are the record of what was done, not a credential.
	keys := a.AuthStore.DeleteAPIKeysOf(user.Username)
	a.AuthStore.DeleteSessionsOf(user.Username, "local_user")
	a.AuthStore.ForgetKnownUser(user.Username)
	directoryChanged()
	logInfo("built-in user deleted by=%s username=%s keys=%d", admin.Str("login"), user.Username, keys)
	writeJSON(w, http.StatusOK, mapOf("ok", true, "deleted_keys", json.Number(strconv.Itoa(keys))), nil)
	return nil
}

func (a *App) createDirectoryGroup(w http.ResponseWriter, r *http.Request) error {
	admin, err := a.admin(r)
	if err != nil {
		return err
	}
	store, err := a.directoryStore()
	if err != nil {
		return err
	}
	payload, err := readJSONObject(r)
	if err != nil {
		return err
	}
	members, err := optionalList(payload, "members")
	if err != nil {
		return err
	}
	group, err := store.CreateGroup(payload.Str("name"), payload.Str("description"), members)
	if err != nil {
		return directoryError(err)
	}
	directoryChanged()
	logInfo("user group created by=%s name=%s members=%d", admin.Str("login"), group.Name, len(group.Members))
	writeJSON(w, http.StatusCreated, mapOf("ok", true, "name", group.Name), nil)
	return nil
}

func (a *App) updateDirectoryGroup(w http.ResponseWriter, r *http.Request) error {
	admin, err := a.admin(r)
	if err != nil {
		return err
	}
	store, err := a.directoryStore()
	if err != nil {
		return err
	}
	payload, err := readJSONObject(r)
	if err != nil {
		return err
	}
	members, err := optionalList(payload, "members")
	if err != nil {
		return err
	}
	group, err := store.UpdateGroup(r.PathValue("name"), optionalString(payload, "description"), members)
	if err != nil {
		return directoryError(err)
	}
	directoryChanged()
	logInfo("user group updated by=%s name=%s members=%d", admin.Str("login"), group.Name, len(group.Members))
	writeJSON(w, http.StatusOK, mapOf("ok", true, "name", group.Name), nil)
	return nil
}

// deleteDirectoryGroup removes the group and its name from every policy that lists it.
//
// Left behind, a policy entry naming a deleted group grants nothing today -- and would silently
// start granting again the day somebody creates a new group of the same name.
func (a *App) deleteDirectoryGroup(w http.ResponseWriter, r *http.Request) error {
	admin, err := a.admin(r)
	if err != nil {
		return err
	}
	store, err := a.directoryStore()
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	group := store.GetGroup(name)
	if group == nil {
		return errorf(http.StatusNotFound, "no such group")
	}
	if err := a.dropGroupFromPolicies(group.Name); err != nil {
		return err
	}
	if err := store.DeleteGroup(group.Name); err != nil {
		return directoryError(err)
	}
	directoryChanged()
	logInfo("user group deleted by=%s name=%s", admin.Str("login"), group.Name)
	writeJSON(w, http.StatusOK, mapOf("ok", true), nil)
	return nil
}

func (a *App) dropGroupFromPolicies(name string) error {
	existing, err := config.LoadRaw()
	if err != nil {
		return errorf(http.StatusInternalServerError, "could not read the configuration: %v", err)
	}
	updates := omap.New()

	if policy := existing.Map("model_policy"); policy != nil {
		if table := policy.Map("user_groups"); table != nil {
			changed := false
			for _, key := range table.Keys() {
				if strings.EqualFold(key, name) {
					table.Delete(key)
					changed = true
				}
			}
			if changed {
				updates.Set("model_policy", policy)
			}
		}
	}

	if authDoc := existing.Map("auth"); authDoc != nil {
		changed := false
		for _, field := range []string{"key_policy", "key_scope_policy"} {
			policy := authDoc.Map(field)
			if policy == nil || !policy.Has("user_groups") {
				continue
			}
			kept := []any{}
			for _, item := range omap.StringSlice(policy.Value("user_groups")) {
				if strings.EqualFold(strings.TrimSpace(item), name) {
					changed = true
					continue
				}
				kept = append(kept, item)
			}
			policy.Set("user_groups", kept)
		}
		if changed {
			// The whole auth section, as every other writer of it does: SaveRaw replaces
			// top-level keys wholesale.
			updates.Set("auth", authDoc)
		}
	}

	if updates.Len() == 0 {
		return nil
	}
	updated, err := config.SaveRaw(updates)
	if err != nil {
		return errorf(http.StatusInternalServerError, "could not save the configuration: %v", err)
	}
	a.setConfig(updated, false)
	modelpolicy.Invalidate()
	return nil
}
