# Numou Identity machine access tokens in Iket

## Contract version

**Contract:** Numou machine access token v1  
**Issuer owner:** Numou Identity  
**Resource-server owner:** Iket  
**Audience:** deployment-specific; configure the same value as Numou's \`NUMOU_OAUTH_ACCESS_TOKEN_AUDIENCE\`.

## Required configuration

Enable the gateway JWT middleware and configure a dedicated Numou RS256 public key:

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

The gateway configuration validator rejects the Numou profile if JWT is disabled, the public key is missing, issuer/audience is missing, or the configured algorithm list is not exactly RS256.

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

Route-level scope authorization remains a separate policy decision. Authentication does not imply authorization for every route.

## Revocation limitation

This profile validates JWTs locally. It does **not** query Numou's credential store and therefore cannot immediately detect credential revocation or rotation after a token has been issued. Tokens remain usable until expiry unless a separate revocation/introspection mechanism is deployed. Keep access-token lifetimes short and do not claim immediate revocation from this local validation alone.

## Key rotation

The deployment must distribute the new public key to Iket before Numou begins signing with it. During a planned overlap, Iket needs a trusted key-selection mechanism (for example, a configured key set keyed by JWT \`kid\`) before multiple keys are used. This single-key profile must not silently accept unknown keys or fall back to HMAC.

## Verification boundary

Unit tests cover the Numou-specific claim contract. Deployment integration tests must additionally prove:
- a token signed by the configured RSA key is accepted;
- wrong signature, wrong issuer, wrong audience and non-RS256 algorithms are rejected;
- missing or malformed machine claims are rejected;
- caller-supplied tenant/application headers are overwritten;
- route scopes are enforced independently;
- revoked/rotated credentials are rejected once the future online revocation contract is integrated.
