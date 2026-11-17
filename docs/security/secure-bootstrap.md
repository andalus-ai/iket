# Secure bootstrap credentials

Iket's generated configuration deliberately contains environment-variable references instead of usable default credentials.

Before starting Iket with the generated configuration, provide strong, unique values through the process environment or a protected `.env` file:

```dotenv
IKET_ADMIN_PASSWORD=replace-with-a-long-random-operator-password
IKET_CLIENT_SECRET=replace-with-a-long-random-client-secret
IKET_API_KEY=replace-with-a-long-random-api-key
# Set only when JWT HS* validation is enabled:
IKET_JWT_SECRET=
```

Use a secret manager in production. Do not commit `.env` files, place secrets in container images, or pass secrets as command-line arguments. Restrict filesystem permissions on local secret files.

The generated Docker Compose configuration requires the admin password, client secret, and API key to be present in the environment before `docker compose up`. The values are passed into the container and expanded when Iket loads its configuration.

If the management password is missing or empty, the management API remains unavailable (HTTP 503). This is intentional fail-closed behavior; an empty password is never accepted as an operator credential.

Iket startup no longer prints the generated configuration files. Use `--print-config` to inspect the loaded configuration; secret fields are redacted by that command.
