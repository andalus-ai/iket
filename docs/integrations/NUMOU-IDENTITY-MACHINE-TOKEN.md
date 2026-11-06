# Numou Identity machine access tokens in Iket

## Contract version

**Contract:** Numou machine access token v1  
**Issuer owner:** Numou Identity  
**Resource-server owner:** Iket  
**Audience:** deployment-specific; configure the same value as Numou's \`NUMOU_OAUTH_ACCESS_TOKEN_AUDIENCE\`.

## Required configuration

Enable the gateway JWT middleware and configure either a dedicated static Numou RS256 public key or the Identity JWKS endpoint. For production rotation, prefer JWKS:

\`\`\`yaml
security:
  jwt:
    enabled: true
    numouAccessTokens: true
    algorithms:
      - RS256
    publicKeyFile: /run/secrets/numou-identity-public.pem
    issuer: https://identity.numou.internal
    audience: numou-iket
\`\`\`

Do not configure the Numou public key as an HMAC secret. Do not enable HS256 fallback for this trust profile. The private key remains in Numou Identity's secret-management boundary and must never be copied to Iket.

The gateway configuration validator rejects the Numou profile if JWT is disabled, both or neither key source is configured, issuer/audience is missing, or the configured algorithm list is not exactly RS256. A JWKS URL must use HTTPS on the configured Identity issuer host.

## Required claims

Iket validates the JWT signature with the configured RSA public key and then requires:

| Claim | Validation |
|---|---|
| \`iss\` | Exact configured issuer |
| \`aud\` | Contains the exact configured resource audience |
| \`sub\` | Must equal \`client_id\` |
| \`client_id\` | Non-empty stable client identifier |
| \`tenant_id\` | UUID; copied into trusted \`X-Tenant-ID\` only after validation |
| \`application_id\` | UUID; copied into trusted \`X-Application-ID\` only after validation |
| \`credential_version\` | Positive integer |
| \`token_use\` | Exactly \`access\` |
| \`scope\` | One to 64 valid space-delimited scopes |
| \`exp\` | Mandatory and not expired |

The JWT parser validates the signature and registered temporal claims. The Numou-specific claim validator adds exact issuer/audience and machine-token semantics.

## Request identity behavior

After successful validation, Iket builds the authenticated principal with client, tenant, application, credential-version, token-use and scope attributes. It overwrites inbound \`X-Tenant-ID\` and \`X-Application-ID\` with values from the verified token. Callers cannot select tenant or application context with headers.

Route-level scope authorization remains a separate policy decision. Authentication does not imply authorization for every route. For Numou machine access tokens (`token_use=access` with a verified `client_id`), every scope configured on the service and route must be present (all-of semantics). Service and route scope requirements are combined, so a token must satisfy the complete set. Existing non-Numou authentication providers retain the gateway's legacy any-of behavior for compatibility. Routes without configured scopes do not gain an implicit scope requirement.

## Online introspection and revocation

For live credential-state enforcement, enable the optional online introspection profile. Iket first verifies the RS256 JWT locally, then calls Numou Identity's internal `POST /internal/v1/oauth2/introspect` endpoint over mutual TLS. Identity remains the authority for credential status, tenant/application ownership, credential version and current scopes.

Example:

```yaml
security:
  jwt:
    enabled: true
    numouAccessTokens: true
    algorithms: [RS256]
    publicKeyFile: /run/secrets/numou-identity-public.pem
    issuer: https://identity.numou.internal
    audience: numou-iket
    introspection:
      enabled: true
      url: https://identity.numou.internal/internal/v1/oauth2/introspect
      tokenUrl: https://identity.numou.internal/api/v1/oauth2/token
      clientId: nmc_iket_introspection
      clientSecretFile: /run/secrets/iket-introspection-client-secret
      workloadScope: oauth-introspect
      caFile: /run/secrets/identity-ca.pem
      clientCertFile: /run/secrets/iket-introspection-client.crt
      clientKeyFile: /run/secrets/iket-introspection-client.key
      timeout: 2s
      positiveCacheTTL: 5s
      negativeCacheTTL: 1s
      cacheMaxEntries: 10000
```

The Iket workload must present both a dedicated mTLS client certificate and a dedicated OAuth client-credentials identity provisioned in Numou with the `oauth-introspect` scope. Iket obtains a short-lived bearer token from the configured token endpoint using the mounted client-secret file, then sends that bearer token over the mTLS channel to introspection. Numou maps the authorized scope into the JWT `groups` role claim consumed by `@RolesAllowed`. The endpoint must not be publicly exposed. Do not reuse a developer's OAuth client secret.

Iket stores only a SHA-256 digest of each raw token as the in-memory cache key. Positive cache TTL is capped at five seconds and never extends beyond JWT expiry; negative TTL is capped at one second. Requests for the same token are coalesced. The cache is bounded and evicts expired/earliest-expiring entries.

A positive cache means revocation is observed within the configured positive TTL plus request/propagation latency, not instantaneously. Identity timeouts, TLS errors, non-200 responses, malformed responses and identity mismatches fail closed with HTTP 503. A confirmed inactive token fails with HTTP 401. If introspection is enabled but client TLS configuration cannot initialize, protected Numou-token requests fail closed.

If online introspection is disabled, the gateway uses local JWT validation only and tokens remain usable until expiry after revocation/rotation.

## JWKS key rotation and outage behavior

For production key rotation, configure the Numou Identity JWKS endpoint instead of a single PEM file. Iket selects the RSA signing key by the JWT header's `kid`; RS256 remains the only accepted algorithm. The JWKS endpoint must be HTTPS and share the configured Identity issuer host. Iket does not follow redirects and does not fall back to HS256 or an untrusted key source.

Example:

```yaml
security:
  jwt:
    enabled: true
    numouAccessTokens: true
    algorithms: [RS256]
    jwksUrl: https://identity.numou.internal/.well-known/jwks.json
    jwksCacheTTL: 5m
    jwksStaleTTL: 15m
    jwksRefreshTimeout: 2s
    jwksMaxKeys: 16
    issuer: https://identity.numou.internal
    audience: numou-iket
```

Operational behavior:

- A successful JWKS response is cached for the configured cache TTL. The response is capped at 64 KiB and the accepted key count is bounded (1–32).
- A token with an unknown `kid` triggers an early refresh even while the current set is fresh. Unknown-key refreshes are serialized and rate-limited to one attempt per five seconds to prevent attacker-controlled network amplification.
- After cache expiry, a known key may be used during an Identity outage only until the configured stale TTL measured from the last successful refresh. Once that bound expires, verification fails closed with HTTP 503.
- An unknown `kid` after a successful refresh is rejected with HTTP 401. If the JWKS source is unavailable and no still-trusted cached key can be used, Iket returns HTTP 503 rather than treating an infrastructure failure as proof of an invalid token.
- A successful refresh replaces the active key set. Keys removed from the published set are no longer accepted after that refresh. Keep old and new signing keys published together during the overlap period, publish the new key before signing with it, and remove the old key only after its maximum token lifetime and rollout overlap have elapsed.
- JWKS removal controls signature trust; it does not provide immediate per-credential revocation. Keep online introspection enabled when revocation and credential-state checks are required.

The static PEM mode remains supported for legacy deployments, but it has no automatic rotation and must be updated out-of-band before a new key signs tokens.

## Verification boundary

Unit tests cover the Numou-specific claim contract. Deployment integration tests must additionally prove:
- a token signed by the configured RSA key is accepted;
- wrong signature, wrong issuer, wrong audience and non-RS256 algorithms are rejected;
- missing or malformed machine claims are rejected;
- caller-supplied tenant/application headers are overwritten;
- route scopes are enforced independently;
- revoked/rotated credentials are rejected on the next uncached introspection request, subject to the configured positive-cache TTL.


## mTLS integration test coverage

Iket's gateway tests include an in-process HTTPS Identity fixture that requires and verifies a client certificate. The fixture exposes both the OAuth token endpoint and introspection endpoint on the same TLS host, checks the dedicated client-credentials grant and `oauth-introspect` scope, checks the separate workload bearer token on introspection, and verifies the submitted application token's active identity and scopes. The test uses an isolated test CA/certificate and temporary secret files; it does not require live Identity credentials. A passing unit/integration test does not replace deployment verification of the production ingress/service-mesh certificate trust and workload role mapping.
