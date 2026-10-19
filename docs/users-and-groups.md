# Built-in users and user groups

GitHub OAuth gives this service two things: who a person is, and (through an enterprise token) which
enterprise, organization and team they belong to. A team that doesn't use GitHub has neither. Before
2.5.0 that left one local super administrator and no way to grant anything to a set of people.

Built-in users and user groups fill that gap. They work alongside GitHub OAuth, not instead of it:

- **Built-in users** are accounts stored in this service. They sign in with a user name and password
  and get API keys, traces and usage like any GitHub account.
- **User groups** are named sets of logins. A group can hold built-in users **and GitHub accounts**.
  Every policy can bind a group the way it binds a GitHub team. Membership is answered locally, so
  no GitHub token is needed.

The console page is **Users** under Management (administrators only), with two tabs: *Users* and
*User groups*. Changes there take effect immediately; there is no save bar.

---

## Built-in users

An administrator creates an account with a user name, an optional display name and email, a first
password, and optionally the administrator role and some groups.

| Field | Notes |
|---|---|
| User name | 1–64 letters, digits, `.`, `_` or `-`, starting with a letter or digit. Fixed after creation, because API keys, traces and policy bindings all key on it. |
| Password | At least 8 characters, and not `admin1234`. Stored as salted scrypt, exactly like the local administrator's. |
| Administrator | A built-in administrator has the same powers as a GitHub administrator: every console page, every user's traces, and the Users page. |
| Require a new password at the next sign-in | On by default. The first sign-in then shows nothing but a change-password form, and every endpoint returns `403 password_change_required` until the user picks their own password. |

**Signing in.** The sign-in page shows the user name and password form whenever the local
administrator is enabled or at least one built-in user is enabled. When GitHub OAuth is configured
too, both are offered. The form signs in the local administrator and built-in users alike, so a
deployment can turn the local administrator off and run on built-in accounts alone.

**Changing a password.** A built-in user changes their own password from the user menu (top right).
That signs out their other sessions and keeps the current one.

**Disabling, resetting, deleting.**

- **Disable** signs the account out at once. Its API keys are refused with `403` until it's enabled
  again.
- **Reset password** sets a new password and signs out every session of the account. It can also
  require the user to pick their own at the next sign-in.
- **Delete** removes the account from every group, ends its sessions, and deletes its API keys. Its
  traces stay, because they are the record of what was done.

An administrator can't disable, demote or delete their own account from the console, so nobody can
lock themselves out.

---

## User groups

A group has a name, an optional description and a member list. Members are logins:

- **Built-in users**, ticked from the list.
- **GitHub accounts that have signed in**, ticked from the list.
- **Any other GitHub login**, typed by hand, for someone who hasn't signed in yet.

Each group card shows where the saved policies use it ("model group: small", "may create keys",
"may narrow keys"). **Deleting a group also removes it from Model policy, Key policy and Key
scope.** Otherwise a policy entry left behind would start granting again the day someone created a
new group with the same name.

---

## Using groups in the policies

| Page | Config key | What a group does |
|---|---|---|
| Model policy | `model_policy.user_groups: {group: model-group}` | Members get that model group, in union with every other grant (default group, user, team, organization). |
| Access control → Key policy | `auth.key_policy.user_groups: [group, ...]` | Members may create API keys. Checked **before** any enterprise rule, and needs no GitHub token. |
| Access control → Key scope | `auth.key_scope_policy.user_groups: [group, ...]` | A fourth level next to user, team and organization: AND across the levels you fill in, OR within one. |
| Administrators and sign-in | (none) | With `allow_any_github_user: false`, a GitHub account that belongs to any user group may still sign in. |

```yaml
model_groups:
  small: [gpt-4.1-mini]
model_policy:
  enabled: true
  user_groups:
    research: small
auth:
  key_policy:
    enabled: true
    user_groups: [research]    # no github_token needed for this to work
  key_scope_policy:
    enabled: true
    user_groups: [research]
```

Built-in users can also be bound **by name**, the same way as a GitHub login: the user table on
Model policy and the user level on Key scope list built-in users, including ones who haven't signed
in yet.

---

## One login namespace

Sessions, API keys, traces and every policy binding key on the login. A built-in user and a GitHub
account with the same name would share all of that, so the service refuses the collision from both
sides:

- Creating a built-in user is refused (`409`) when the name is the local administrator's user name,
  a GitHub login in `auth.admin_logins`, or a GitHub account that has already signed in.
- A GitHub sign-in is refused when its login is a built-in user. The sign-in page shows why.
- A built-in user is never looked up on GitHub. Organization and team bindings don't apply to them,
  even if a GitHub account with the same name exists and belongs to that organization.

---

## Storage

Built-in users and groups live in `data/directory.json`, not in `config.yaml`. The file holds
password hashes and is written with mode `0600`. Like every other file under `data/`, it is re-read
when it changes, so several workers sharing `data/` see each other's edits. Back it up with the rest
of `data/`.

A forgotten administrator password can be recovered in two ways: reset it from another
administrator's session, or sign in as the local administrator. With the local administrator
disabled and no other administrator, re-enable it in `config.yaml` (`auth.local_admin.enabled:
true`) and restart.

---

## API

All administrator-only except `POST /v1/auth/password`.

| Endpoint | Body | Notes |
|---|---|---|
| `GET /v1/directory` | | Users (never hashes), groups with their policy references, and the GitHub accounts that have signed in. |
| `POST /v1/directory/users` | `{username, password, display_name?, email?, is_admin?, must_change_password?, groups?}` | `201`. `409` on a name collision, `422` on a bad name or password. |
| `PATCH /v1/directory/users/{username}` | any of `{display_name, email, is_admin, disabled, must_change_password, password, groups}` | `password` resets it and signs out every session of the account. `409` when an administrator demotes, disables or deletes themselves. |
| `DELETE /v1/directory/users/{username}` | | Also deletes the user's API keys and sessions. |
| `POST /v1/directory/groups` | `{name, description?, members?}` | `201`. |
| `PATCH /v1/directory/groups/{name}` | `{description?, members?}` | `members` replaces the list. |
| `DELETE /v1/directory/groups/{name}` | | Also removes the group from the three policies. |
| `POST /v1/auth/local/login` | `{username, password}` | Signs in the local administrator or a built-in user. `403` (after a correct password) for a disabled account. |
| `POST /v1/auth/password` | `{current_password, new_password}` | A built-in user changes their own password. Reachable while the forced change is pending. |

`GET /v1/auth/status` also reports `local_login_enabled` (the password form has an account behind
it) and `builtin_users` (at least one enabled built-in user exists).
