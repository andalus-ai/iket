# Management API roles

Iket management API requests under `/api/v1` require verified mutual TLS and
Basic Auth. RBAC is an additional authorization layer; it does not replace
either authentication requirement.

Configure a role for each Basic Auth username:

```yaml
security:
  enableBasicAuth: true
  basicAuthUsers:
    read-operator: "${IKET_READ_PASSWORD}"
    deploy-operator: "${IKET_OPERATOR_PASSWORD}"
    platform-admin: "${IKET_ADMIN_PASSWORD}"
  basicAuthRoles:
    read-operator: viewer
    deploy-operator: operator
    platform-admin: admin
```

## Roles

- **viewer** — read-only management endpoints, including logs, metrics,
  status, route/service details, and read-only proposal/revision views.
- **operator** — viewer access plus operational mutations such as route and
  service changes, reloads, notifications, and proposal workflow actions.
- **admin** — all management operations, including gateway configuration,
  plugin lifecycle/configuration, certificates, enrollment tokens, client
  credentials, backups, and restore operations.

Sensitive gateway configuration reads are admin-only. The same policy applies
to REST, server-sent events, and WebSocket endpoints because authorization is
enforced in the shared management middleware.

## Compatibility and migration

For backward compatibility, authenticated usernames omitted from
`basicAuthRoles` retain the previous full-management access and are treated
as `admin`. This is a migration aid, not the recommended steady state.
Explicitly assign every account a role and remove unnecessary admin access.
Unknown role values are denied.

Denied authorization decisions and authorized mutations are written to the
gateway structured log with username, role, method, path, and required role
(where applicable). Passwords and Authorization headers are never logged.

CORS preflight requests remain handled before authentication and do not grant
access to management resources.
