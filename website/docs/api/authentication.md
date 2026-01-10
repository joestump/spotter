---
sidebar_position: 2
---

# Authentication

Spotter uses Navidrome credentials for authentication.

## Web Authentication

### Login Flow

1. Navigate to `http://localhost:8080`
2. Enter your Navidrome username and password
3. Spotter authenticates against your Navidrome instance
4. A session cookie is set for subsequent requests

### Session Management

- Sessions are stored server-side
- Session cookies are HTTP-only for security
- Sessions expire after inactivity (configurable)

## API Authentication

API requests require an active session. The recommended flow:

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

## OAuth Providers

### Spotify

Spotify uses OAuth 2.0:

1. User clicks "Connect Spotify"
2. Redirected to Spotify authorization
3. After authorization, redirected back with code
4. Spotter exchanges code for tokens
5. Tokens stored in database

### Last.fm

Last.fm uses web authentication:

1. User clicks "Connect Last.fm"
2. Redirected to Last.fm authorization
3. After authorization, redirected back with token
4. Session key stored in database

## Security Considerations

### Passwords

- Passwords are never stored by Spotter
- Authentication is delegated to Navidrome
- Session tokens are used after initial auth

### Tokens

- OAuth tokens are stored encrypted
- Refresh tokens are used when access tokens expire
- Tokens can be revoked by disconnecting services

### HTTPS

For production deployments:

- Always use HTTPS
- Set secure cookie flags
- Use a reverse proxy (nginx, Traefik)

## Troubleshooting

### "Invalid credentials"

1. Verify your Navidrome credentials
2. Check Navidrome is accessible
3. Ensure the user has API access

### "Session expired"

1. Log in again
2. Check session timeout settings
3. Verify cookies are being sent

### OAuth Errors

1. Check redirect URIs match exactly
2. Verify client credentials
3. Ensure HTTPS for production
