# Admin Password Change - Design Spec

**Date:** 2026-04-13  
**Scope:** Self-service password rotation for authenticated instance admins  
**Repos affected:** `hush-server` (backend + embedded admin UI)

---

## Problem

There is no safe path for an instance admin to rotate their own password. The only existing recovery path is a direct DB write, which is unsafe for production use. This spec closes that gap with a minimal, secure self-service flow.

---

## Session Behavior (explicit)

**The current session remains valid after a password change.**

This is a self-initiated, authenticated, verified rotation - not an incident-response forced reset. Invalidating the current session after a successful self-service change would be operationally disruptive without a meaningful security benefit in this context.

- Old password stops working immediately after the DB write.
- New password is required for all future logins.
- The session cookie issued before the change continues to work until it expires naturally.
- No global session sweep is performed.

---

## Backend

### New endpoint

```
POST /api/admin/session/change-password
```

**Middleware:** `RequireAdminSession`, `RequireAdminOrigin`  
**Role requirement:** None - any active admin can change their own password.

### Request body

```json
{
  "currentPassword": "string",
  "newPassword": "string"
}
```

### Handler logic

1. Decode and validate body - return `400` on malformed JSON or empty fields.
2. Load admin by ID from context (`adminIDFromContext`).
3. Call `auth.VerifyAdminPassword(currentPassword, admin.PasswordHash)`.
   - Verification error (internal) -> `500`
   - Password does not match -> `401 {"error": "current password is incorrect"}`
4. Reject `newPassword == currentPassword` -> `400 {"error": "new password must differ from current password"}`
5. Validate `len(newPassword) >= 12` -> `400 {"error": "password must be at least 12 characters"}`
6. `auth.HashAdminPassword(newPassword)` - Argon2id, same parameters as all other admin password hashes.
7. `store.UpdateInstanceAdminPassword(ctx, admin.ID, newHash)` - failure -> `500`.
8. Return `204 No Content`.

**The session cookie is not read, modified, or deleted by this handler.** It is already validated by `RequireAdminSession` upstream; the handler only touches the password hash.

### Route registration

Add alongside existing session routes in `AdminAPIRoutes` in `admin.go`:

```go
r.With(RequireAdminSession(store), RequireAdminOrigin()).
    Post("/session/change-password", h.changePassword)
```

### Request/response types

New in `admin_auth.go`:

```go
type adminChangePasswordRequest struct {
    CurrentPassword string `json:"currentPassword"`
    NewPassword     string `json:"newPassword"`
}
```

---

## Frontend

### New component: `ChangePasswordModal.jsx`

**Location:** `hush-server/admin/src/ChangePasswordModal.jsx`

**Props:** `{ isOpen, onClose }`  
Renders nothing when `isOpen` is false.

**State:**
- `currentPassword`, `newPassword`, `confirmPassword` - form fields
- `error` - server or validation error message
- `submitting` - boolean, true while request is in flight
- `success` - boolean, true after successful change (triggers auto-close)

**Behavior:**
- Client-side guards (pre-submit):
  - New password and confirm must match.
  - New password must be >= 12 characters.
  - These are UX guards only; the server is authoritative.
- Submit button is disabled while `submitting` is true.
- Backdrop click and Escape key close the modal **only when `submitting` is false**.
- On success: set `success = true`, wait 1.2 s, then call `onClose()`.
- `onClose()` resets all form state (currentPassword, newPassword, confirmPassword, error, success, submitting = false).

**Styling:** uses existing CSS variables (`--bg`, `--surface`, `--elevated`, `--border`, `--text`, `--text-muted`, `--accent`, `--danger`) and utility classes (`.btn`, `.btn-primary`, `.btn-secondary`, `.input`). No new CSS file.

### `adminApi.js` addition

```js
export async function changePassword({ currentPassword, newPassword }) {
  await request('/session/change-password', {
    method: 'POST',
    body: JSON.stringify({ currentPassword, newPassword }),
  });
}
```

### `App.jsx` changes

- Import `ChangePasswordModal` and `changePassword`.
- Add `isChangingPassword` boolean state.
- Add a "Change password" text button in the header `controls` section (between the badge and the logout button).
- Render `<ChangePasswordModal isOpen={isChangingPassword} onClose={() => setIsChangingPassword(false)} />`.

---

## Tests

### Backend (`admin_test.go`)

| Test name | Scenario | Expected |
|-|-|-|
| `TestAdminChangePassword_RequiresSession` | No session cookie | 401 |
| `TestAdminChangePassword_MissingFields_Returns400` | Empty body | 400 |
| `TestAdminChangePassword_WrongCurrentPassword_Returns401` | Wrong current password | 401 |
| `TestAdminChangePassword_SameAsCurrent_Returns400` | newPassword == currentPassword | 400 |
| `TestAdminChangePassword_WeakNewPassword_Returns400` | New password < 12 chars | 400 |
| `TestAdminChangePassword_Success_Returns204` | Correct current, valid distinct new | 204; stored hash verified via `auth.VerifyAdminPassword(newPassword, storedHash)` |

The success test must verify that `auth.VerifyAdminPassword(newPassword, capturedHash)` returns `true`, confirming correct Argon2id encoding - not just that the store method was called.

### Manual verification steps

After implementation, verify:

1. Log in with the current password. Note the session cookie is active.
2. POST `change-password` with the correct current password and a new password.
3. Attempt to log in with the **old password** -> must fail (401).
4. Attempt to log in with the **new password** -> must succeed (200, session cookie issued).
5. Confirm the **existing session** from step 1 still authorizes `GET /session/me` -> must return 200.

---

## Files changed

| File | Change |
|-|-|
| `hush-server/internal/api/admin_auth.go` | Add `changePassword` handler and `adminChangePasswordRequest` type |
| `hush-server/internal/api/admin.go` | Register new route |
| `hush-server/internal/api/admin_test.go` | Add 6 new tests |
| `hush-server/admin/src/ChangePasswordModal.jsx` | New file |
| `hush-server/admin/src/lib/adminApi.js` | Add `changePassword` export |
| `hush-server/admin/src/App.jsx` | Add modal trigger in header controls |

---

## Out of scope

- Global session invalidation / revocation on password change.
- Broader account settings (email change, display name, etc.).
- Password complexity rules beyond the existing 12-character minimum.
- Admin UI tests (the UI is a thin form delegating to the verified backend; backend tests are the meaningful coverage here).
