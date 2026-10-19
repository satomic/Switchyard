package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/satomic/model-router/server-go/internal/authstore"
	"github.com/satomic/model-router/server-go/internal/config"
	"github.com/satomic/model-router/server-go/internal/directory"
	"github.com/satomic/model-router/server-go/internal/modelpolicy"
)

const directoryFixture = `providers:
  stub:
    base_url: http://127.0.0.1:9/
    api_type: openai
models:
  small-model:
    provider: stub
    default: true
  big-model:
    provider: stub
model_groups:
  small: [small-model]
model_policy:
  enabled: true
  user_groups:
    research: small
auth:
  github: {client_id: '', client_secret: ''}
  admin_logins: [gh-admin]
  local_admin:
    enabled: true
    username: admin
`

// directoryHarness runs the real route table over a temporary data directory.
type directoryHarness struct {
	t      *testing.T
	server *httptest.Server
	store  *directory.Store
}

func newDirectoryHarness(t *testing.T) *directoryHarness {
	t.Helper()
	dir := t.TempDir()
	savedPath := config.ConfigPath
	config.ConfigPath = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config.ConfigPath, []byte(directoryFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	store := directory.New(dir)
	directory.Use(store)
	modelpolicy.Invalidate()
	t.Cleanup(func() {
		directory.Use(nil)
		config.ConfigPath = savedPath
		modelpolicy.Invalidate()
	})
	app := New(cfg, nil, authstore.New(dir), "")
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	return &directoryHarness{t: t, server: server, store: store}
}

// client is one browser: its own cookie jar.
func (h *directoryHarness) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func (h *directoryHarness) call(c *http.Client, method, path string, body any, headers ...string) (int, map[string]any) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func (h *directoryHarness) expect(c *http.Client, want int, method, path string, body any, headers ...string) map[string]any {
	h.t.Helper()
	status, out := h.call(c, method, path, body, headers...)
	if status != want {
		h.t.Fatalf("%s %s = %d %v, want %d", method, path, status, out, want)
	}
	return out
}

func (h *directoryHarness) login(username, password string) *http.Client {
	h.t.Helper()
	c := h.client()
	h.expect(c, 200, "POST", "/v1/auth/local/login", map[string]any{"username": username, "password": password})
	return c
}

func modelIDs(out map[string]any) []string {
	ids := []string{}
	data, _ := out["data"].([]any)
	for _, item := range data {
		ids = append(ids, item.(map[string]any)["id"].(string))
	}
	return ids
}

func TestBuiltinUsersEndToEnd(t *testing.T) {
	h := newDirectoryHarness(t)
	if _, err := h.store.CreateUser(directory.NewUser{Username: "root", Password: "root-password", IsAdmin: true}); err != nil {
		t.Fatal(err)
	}
	root := h.login("root", "root-password")
	status := h.expect(root, 200, "GET", "/v1/auth/status", nil)
	if user := status["user"].(map[string]any); user["is_admin"] != true || user["local_user"] != true {
		t.Fatalf("root's session = %v", user)
	}
	if status["local_login_enabled"] != true {
		t.Error("the password form must be offered while built-in users exist")
	}

	// -- Name collisions with the other identity sources are refused --
	h.expect(root, 409, "POST", "/v1/directory/users", map[string]any{"username": "admin", "password": "long enough"})
	h.expect(root, 409, "POST", "/v1/directory/users", map[string]any{"username": "gh-admin", "password": "long enough"})
	h.expect(root, 422, "POST", "/v1/directory/users", map[string]any{"username": "bob", "password": "short"})

	// -- An administrator creates a user and a group --
	h.expect(root, 201, "POST", "/v1/directory/users", map[string]any{
		"username": "bob", "display_name": "Bob", "password": "initial-pass",
	})
	h.expect(root, 201, "POST", "/v1/directory/groups", map[string]any{
		"name": "research", "description": "the lab", "members": []string{"bob", "octocat"},
	})
	dir := h.expect(root, 200, "GET", "/v1/directory", nil)
	if users := dir["users"].([]any); len(users) != 2 {
		t.Fatalf("directory users = %v", users)
	}
	group := dir["groups"].([]any)[0].(map[string]any)
	if refs := group["references"].(map[string]any); refs["model_group"] != "small" {
		t.Errorf("the group's model-policy binding was not reported: %v", refs)
	}

	// -- First sign-in is held at the change-password form --
	bob := h.login("bob", "initial-pass")
	if out := h.expect(bob, 403, "GET", "/v1/keys", nil); out["detail"] != "password_change_required" {
		t.Fatalf("a forced change should gate everything, got %v", out)
	}
	h.expect(bob, 422, "POST", "/v1/auth/password", map[string]any{"current_password": "wrong", "new_password": "bobs-own-pass"})
	h.expect(bob, 200, "POST", "/v1/auth/password", map[string]any{"current_password": "initial-pass", "new_password": "bobs-own-pass"})
	h.expect(bob, 200, "GET", "/v1/keys", nil)

	// -- The user group decides the model list --
	key := h.expect(bob, 200, "POST", "/v1/keys", map[string]any{"name": "laptop"})
	plaintext, _ := key["key"].(string)
	if plaintext == "" {
		t.Fatalf("no plaintext key in %v", key)
	}
	bearer := []string{"Authorization", "Bearer " + plaintext}
	if got := modelIDs(h.expect(h.client(), 200, "GET", "/v1/models", nil, bearer...)); len(got) != 1 || got[0] != "small-model" {
		t.Errorf("bob's models = %v, want only the research group's small-model", got)
	}

	// -- Key policy: a user group is an allow list that works without any GitHub token --
	cfg := h.expect(root, 200, "GET", "/v1/config", nil)
	authDoc := cfg["auth"].(map[string]any)
	authDoc["key_policy"] = map[string]any{"enabled": true, "user_groups": []string{}}
	h.expect(root, 200, "PUT", "/v1/config", map[string]any{"auth": authDoc})
	if me := h.expect(bob, 200, "GET", "/v1/access/me", nil); me["allowed"] != false {
		t.Errorf("with no group allowed bob must not create keys: %v", me)
	}
	authDoc["key_policy"] = map[string]any{"enabled": true, "user_groups": []string{"research"}}
	authDoc["key_scope_policy"] = map[string]any{"enabled": true, "user_groups": []string{"research"}}
	h.expect(root, 200, "PUT", "/v1/config", map[string]any{"auth": authDoc})
	me := h.expect(bob, 200, "GET", "/v1/access/me", nil)
	if me["allowed"] != true || me["reason_code"] != "memberUserGroup" {
		t.Errorf("membership of an allowed group should allow key creation: %v", me)
	}
	if scope := me["key_scope"].(map[string]any); scope["allowed"] != true {
		t.Errorf("the user_group level of the key-scope policy did not match: %v", scope)
	}

	// -- Disabling stops the console session and the API key at once --
	h.expect(root, 200, "PATCH", "/v1/directory/users/bob", map[string]any{"disabled": true})
	if out := h.expect(bob, 200, "GET", "/v1/auth/status", nil); out["authenticated"] != false {
		t.Error("a disabled user's session is still signed in")
	}
	h.expect(h.client(), 403, "GET", "/v1/models", nil, bearer...)
	h.expect(h.client(), 403, "POST", "/v1/auth/local/login", map[string]any{"username": "bob", "password": "bobs-own-pass"})

	// -- A password reset ends existing sessions --
	h.expect(root, 200, "PATCH", "/v1/directory/users/bob", map[string]any{"disabled": false})
	bob = h.login("bob", "bobs-own-pass")
	h.expect(root, 200, "PATCH", "/v1/directory/users/bob", map[string]any{"password": "reset-by-admin", "must_change_password": false})
	if out := h.expect(bob, 200, "GET", "/v1/auth/status", nil); out["authenticated"] != false {
		t.Error("a session opened before a password reset survived it")
	}
	bob = h.login("bob", "reset-by-admin")

	// -- Administrators cannot lock themselves out --
	h.expect(root, 409, "PATCH", "/v1/directory/users/root", map[string]any{"is_admin": false})
	h.expect(root, 409, "PATCH", "/v1/directory/users/root", map[string]any{"disabled": true})
	h.expect(root, 409, "DELETE", "/v1/directory/users/root", nil)
	h.expect(bob, 403, "GET", "/v1/directory", nil)

	// -- Promotion takes effect on the next request --
	h.expect(root, 200, "PATCH", "/v1/directory/users/bob", map[string]any{"is_admin": true})
	h.expect(bob, 200, "GET", "/v1/directory", nil)

	// -- Deleting a group removes it from every policy --
	h.expect(root, 200, "DELETE", "/v1/directory/groups/research", nil)
	cfg = h.expect(root, 200, "GET", "/v1/config", nil)
	if bindings, _ := cfg["model_policy"].(map[string]any)["user_groups"].(map[string]any); len(bindings) != 0 {
		t.Errorf("model_policy.user_groups still holds %v", bindings)
	}
	authAfter := cfg["auth"].(map[string]any)
	for _, field := range []string{"key_policy", "key_scope_policy"} {
		if list, _ := authAfter[field].(map[string]any)["user_groups"].([]any); len(list) != 0 {
			t.Errorf("auth.%s.user_groups still holds %v", field, list)
		}
	}

	// -- Deleting a user takes their keys and sessions with them --
	h.expect(root, 200, "DELETE", "/v1/directory/users/bob", nil)
	h.expect(h.client(), 401, "GET", "/v1/models", nil, bearer...)
	if out := h.expect(bob, 200, "GET", "/v1/auth/status", nil); out["authenticated"] != false {
		t.Error("a deleted user's session is still signed in")
	}
}
