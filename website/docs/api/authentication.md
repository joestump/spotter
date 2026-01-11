---
sidebar_position: 2
---

# Authentication

Spotter uses Navidrome credentials for authentication, with JWT (JSON Web Tokens) for session management.

## Web Authentication

### Login Flow

1. Navigate to `http://localhost:8080`
2. Enter your Navidrome username and password
3. Spotter authenticates against your Navidrome instance via the Subsonic API
4. A signed JWT is issued and stored in a secure cookie

### Session Management

Spotter uses JWT-based authentication:

- **Token-based**: Sessions are encoded in signed JWT tokens (not stored server-side)
- **Secure cookies**: Tokens are stored in `HttpOnly`, `Secure`, `SameSite=Strict` cookies
- **24-hour expiry**: Tokens automatically expire after 24 hours
- **Signed with HMAC-SHA256**: Tokens are cryptographically signed and cannot be forged

:::info Cookie Security
The authentication cookie (`spotter_token`) is configured with maximum security settings:
- `HttpOnly`: Prevents JavaScript access (XSS protection)
- `Secure`: Only sent over HTTPS (when configured)
- `SameSite=Strict`: Prevents CSRF attacks
:::

### JWT Claims

Each token contains:

| Claim | Description |
| :--- | :--- |
| `uid` | User ID (integer) |
| `usr` | Username (string) |
| `iss` | Issuer (`spotter`) |
| `sub` | Subject (user ID as string) |
| `iat` | Issued at timestamp |
| `exp` | Expiration timestamp (24h from issue) |
| `nbf` | Not before timestamp |

## API Authentication

API requests require a valid JWT cookie. The recommended flow:

### Using Session Cookies

1. Log in via the web interface
2. Use the session cookie for API requests

```bash
# Login and save cookies
curl -c cookies.txt -X POST http://localhost:8080/login \
  -d "username=your_user&password=your_pass"

# Make authenticated request
curl -b cookies.txt http://localhost:8080/api/listens
```

### HTMX Requests

HTMX requests automatically include session cookies when made from the browser.

:::tip Automatic Redirect
When a JWT expires or is invalid, the middleware automatically:
1. Clears the invalid cookie
2. Redirects to `/auth/login`
3. For HTMX requests, sends an `HX-Redirect` header instead
:::

## OAuth Providers

### Spotify

Spotify uses OAuth 2.0:

1. User clicks "Connect Spotify"
2. Redirected to Spotify authorization
3. After authorization, redirected back with code
4. Spotter exchanges code for tokens
5. Tokens stored encrypted in database

### Last.fm

Last.fm uses web authentication:

1. User clicks "Connect Last.fm"
2. Redirected to Last.fm authorization
3. After authorization, redirected back with token
4. Session key stored encrypted in database

## Security Configuration

### JWT Secret

:::danger Required Configuration
You must set a strong JWT secret for production deployments:

```bash
# Generate a secure secret (32+ characters required)
openssl rand -base64 32

# Set in environment
SPOTTER_SECURITY_JWT_SECRET=your-generated-secret-here
```

The secret must be at least 32 characters. Spotter will refuse to start without a valid secret.
:::

### Encryption Key

OAuth tokens are encrypted at rest:

```bash
# Generate encryption key
openssl rand -hex 32

# Set in environment
SPOTTER_SECURITY_ENCRYPTION_KEY=your-64-char-hex-key
```

### Secure Cookies

For production with HTTPS:

```bash
SPOTTER_SECURITY_SECURE_COOKIES=true
```

:::warning Development Mode
Set `SPOTTER_SECURITY_SECURE_COOKIES=false` only for local HTTP development. Never disable in production.
:::

## Security Considerations

### Passwords

- Passwords are never stored by Spotter
- Authentication is delegated to Navidrome via the Subsonic API
- Navidrome credentials are stored encrypted for background sync operations

### Tokens

- JWT tokens are cryptographically signed (HMAC-SHA256)
- OAuth tokens are stored encrypted with AES-256
- Refresh tokens are used when access tokens expire
- Tokens can be revoked by disconnecting services

### HTTPS

For production deployments:

- Always use HTTPS
- Ensure `SPOTTER_SECURITY_SECURE_COOKIES=true`
- Use a reverse proxy (nginx, Traefik, Caddy)

## Troubleshooting

### "Invalid credentials"

1. Verify your Navidrome credentials
2. Check Navidrome is accessible from Spotter
3. Ensure the user has API access in Navidrome

### "Session expired" / Redirect to login

1. Your JWT has expired (24-hour limit) - log in again
2. The JWT secret may have changed (invalidates all tokens)
3. Verify cookies are being sent with requests

### Token Validation Errors

If you see JWT validation errors in logs:

1. Ensure `SPOTTER_SECURITY_JWT_SECRET` is set consistently
2. Check that the secret is at least 32 characters
3. Verify the cookie is not corrupted

:::note Secret Rotation
Changing `SPOTTER_SECURITY_JWT_SECRET` invalidates all existing sessions. Users will need to log in again.
:::

### OAuth Errors

1. Check redirect URIs match exactly
2. Verify client credentials are correct
3. Ensure HTTPS for production OAuth flows
