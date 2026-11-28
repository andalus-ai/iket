# Configuration revisions and optimistic concurrency

The management gateway configuration endpoint exposes a strong, content-addressed
ETag. The value is a SHA-256 fingerprint of the JSON representation of the
configuration (including its current secret-bearing internal representation;
the redacted GET response does not expose secret values).

## Read

`GET /api/v1/gateway/config` returns the redacted configuration and an
`ETag: "sha256:<digest>"` response header.

## Conditional update

Send the ETag received from GET in `If-Match` when updating:

```http
PUT /api/v1/gateway/config
If-Match: "sha256:<digest>"
Content-Type: application/json
```

A matching tag allows the update. If the current configuration has changed,
Iket returns HTTP 409 with the current ETag in the response header and
`current_revision` / `expected_revision` in the structured error details.
Clients should re-fetch, reconcile their intended changes, and retry.

For backward compatibility, updates that omit `If-Match` remain accepted as
unconditional writes. New clients should always send it. The wildcard form
`If-Match: *` is supported; weak ETags are not.

The comparison is performed while holding the management API's in-process
configuration mutation lock. Because the ETag is content-addressed, it also
detects changes made by another Iket instance when both instances read the
same updated live configuration. This endpoint does not provide a distributed
compare-and-swap transaction across independent processes or external storage
writers. Durable cross-process write serialization remains the responsibility
of the configuration provider / deployment topology.


## Snapshot history and rollback

Iket stores complete configuration snapshots for revision history under
`.iket-admin/revisions`. The existing management endpoints provide:

- `GET /api/v1/revisions` — list revision metadata.
- `GET /api/v1/revisions/{id}` — inspect a stored revision.
- `GET /api/v1/revisions/diff?from={id}&to={id}` — compare two revisions
  (the special `current` source resolves to the current live configuration).
- `POST /api/v1/revisions/{id}/restore` — restore a revision.

Revision and backup snapshots contain the full configuration, which may include
credentials and other secrets. The revision endpoints therefore require the
admin role, and snapshot directories/files are created with owner-only
permissions (`0700` directories and `0600` files). Operators should keep
`.iket-admin` on encrypted, access-controlled storage and exclude it from
source control and public artifact bundles.

New revision records include a SHA-256 configuration fingerprint. Iket verifies
that fingerprint when loading a revision and refuses to restore a modified
snapshot. Older revision records without a fingerprint remain readable for
backward compatibility. Snapshot writes use a temporary file, sync it, and
rename it into place so readers do not observe a partially written JSON file.

A revision restore is conditional when the caller supplies `If-Match`. A stale
ETag returns HTTP 409 without changing the live configuration. Before a
successful restore, Iket writes a separate `pre_restore_checkpoint` revision
of the current configuration; the restore itself is then recorded as a new
revision. The target snapshot is validated before activation.

Revision storage remains local to the gateway's configured working directory.
It is not a distributed revision database, and local filesystem access remains
inside the trust boundary. Existing backup restore is retained for compatibility;
revision restore is the preferred path when audit history and rollback
checkpoints are required.
