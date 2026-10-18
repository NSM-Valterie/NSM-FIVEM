# NSM Sign-In & Clean-Room Server Admin Panel (`tools/nsm-admin`)

Additive, clean-room implementation addressing the Master Prompt backlog item:
> *"NSM sign-in and Admin panel: replace any remaining Discord-dependent sign-in only with a supported NSM-owned authentication design; build a new NSM Admin panel rather than rebranding txAdmin."*

## Architecture & Security Properties

1. **Zero `txAdmin` or Discord OAuth dependency:**
   - Uses native NSM operator accounts (`authProvider: "nsm-native"`) with 200,000-iteration PBKDF2-HMAC-SHA256 password hashing and 128-bit per-account random salts.
   - Issues time-bounded `nsm_sess_*` session tokens paired with per-session CSRF tokens (`X-NSM-CSRF-Token`) required on all state-changing endpoints.
2. **Integrated with `tools/nsm-keyservice/`:**
   - Shares `KeyStore` and `AuthenticatedRegistry` so operators can manage only the dedicated servers registered under their own NSM account (`403 forbidden_not_server_owner` on cross-account IDOR attempts).
3. **Redacted Audit Log:**
   - Records account registration, login successes/failures, denied IDOR attempts, and queued server actions while automatically redacting any `nsm_srv_*` key material.
4. **Locked v0.7.7 Paths Untouched:**
   - 100% additive under `tools/nsm-admin/`.

## Run Verification Tests

```bash
python3 -m unittest discover -s tools/nsm-admin -v
```
