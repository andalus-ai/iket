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
