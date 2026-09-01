# Production security and key migration

This document covers the Keygate application contract. Production Compose,
backup, upgrade, rollback, image digest, and network procedures are maintained
in `digital-warehousing-control-plane/deploy/keygate/README.md`.

## Required production configuration

Production startup rejects missing or weak `REDIS_URL`, `OTP_PEPPER`,
`METRICS_TOKEN`, `RELEASE_KEY_ENCRYPTION_KEY`, `JWT_SECRET`, or
`LICENSE_SIGNING_KEY`. Generate independent random values; never reuse one
secret for another purpose. `/ready` and `/metrics` require
`Authorization: Bearer <METRICS_TOKEN>`. `/health` exposes only liveness.

Customer email OTP is unavailable when SMTP is not configured. Codes are never
logged. Local owner/admin password login remains available without SMTP.
Password changes, account recovery, and recovery-code rotation increment the
server-side session generation, so every older JWT and refresh token is revoked
while the newly authenticated browser receives one replacement session.

For an existing installation upgraded from a version without local passwords,
sign in through email OTP while SMTP is available, then call
`POST /api/v1/admin/account/password` with `new_password` and no
`current_password`. This exception is accepted only while that administrator's
password hash is empty. Afterward, all password changes require the current
password; rotate recovery codes and store the returned codes offline before
relying on password-only emergency access.

## One-time initialization

Setup is closed unless `SETUP_ENABLED=true` and a strong `BOOTSTRAP_SECRET` is
configured. Send `POST /api/v1/setup/initialize` with that Bearer secret and an
`admin_password` of 16 to 128 characters. The response returns recovery codes
once and carries `Cache-Control: no-store`; store them offline.

Concurrent initialization is serialized by a PostgreSQL transaction advisory
lock. The committed owner/setup state consumes the bootstrap capability. After
success, set `SETUP_ENABLED=false` and remove `BOOTSTRAP_SECRET` from runtime
configuration.

## Two-stage license-key migration

Start with `LICENSE_KEY_STORAGE_MODE=dual`. Keygate takes a PostgreSQL advisory
lock, validates or backfills every license hash/ciphertext, rotates license and
release-signing ciphertext to the current master key, and retains plaintext for
the rollback window. During master-key rotation, set the old value only in
`RELEASE_KEY_ENCRYPTION_KEY_PREVIOUS`; clear it after validation.

Before switching to `ciphertext_only`, create and verify the encrypted database
backup required by the control-plane upgrade procedure. Startup then verifies
that every protected row decrypts, clears license plaintext, removes the legacy
plaintext index/unique constraint, and adds a validated ciphertext-only check.
The database records this state and refuses a later `dual` startup.

After `ciphertext_only`, an old image alone is not a valid rollback. Recovery is
limited to restoring the verified database backup from before the transition.

## Signing without artifact storage

`RELEASE_KEY_ENCRYPTION_KEY` enables release signing-key generation, rotation,
listing, and public-key export even when S3/MinIO is absent. Artifact upload,
download, and server-side artifact signing return `503 STORAGE_DISABLED` until
object storage is configured. Missing master-key configuration is reported as
`SIGNING_KEY_UNAVAILABLE`, separately from storage availability.
