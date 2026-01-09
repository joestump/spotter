# Security

## Data Encryption at Rest

Spotter encrypts sensitive data at rest using AES-256-GCM encryption. The following data is automatically encrypted:

- **Navidrome passwords** (used for Subsonic API authentication)
- **Spotify OAuth tokens** (access and refresh tokens)
- **Last.fm session keys**

### Setup

#### Generate an Encryption Key

To enable encryption, you must generate a secure 32-byte (256-bit) encryption key. You can generate one using OpenSSL:

```bash
openssl rand -hex 32
```

This will output a 64-character hexadecimal string like:
```
a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456
```

#### Configure the Encryption Key

Set the encryption key as an environment variable:

```bash
export SPOTTER_SECURITY_ENCRYPTION_KEY="a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456"
```

Or add it to your configuration file if using file-based configuration.

**Important:**
- The key MUST be exactly 64 hexadecimal characters (representing 32 bytes)
- Keep this key secret and secure
- Back up this key in a secure location - you cannot decrypt your data without it
- If you lose the key, all encrypted data will be unrecoverable

### How It Works

Encryption and decryption happen automatically and transparently:

1. **On Write**: When passwords or tokens are saved to the database, they are automatically encrypted using AES-256-GCM before being written
2. **On Read**: When data is loaded from the database, it is automatically decrypted before being returned to your application
3. **Backward Compatibility**: Existing plaintext data is handled gracefully - it will be encrypted the next time it's updated

No code changes are required - the encryption hooks are registered automatically when the database client is created.

### Security Properties

- **Algorithm**: AES-256-GCM (Galois/Counter Mode)
- **Key Size**: 256 bits (32 bytes)
- **Authentication**: GCM mode provides authenticated encryption, protecting against tampering
- **Randomization**: Each encryption uses a unique random nonce, so encrypting the same data twice produces different ciphertexts
- **Storage Format**: Encrypted data is base64-encoded for safe storage in text fields

### Migration from Plaintext

If you're enabling encryption on an existing installation with plaintext passwords:

1. Set the `SPOTTER_SECURITY_ENCRYPTION_KEY` environment variable
2. Restart the application
3. Existing plaintext data will be read correctly (backward compatible)
4. The next time each password/token is updated, it will be automatically encrypted
5. To force encryption of all existing data, you can trigger a password update for each user

### Key Rotation

To rotate your encryption key:

1. This is not currently supported - key rotation would require decrypting all data with the old key and re-encrypting with the new key
2. If you need to rotate keys, you'll need to implement a migration script that:
   - Reads all encrypted data with the old key
   - Re-encrypts it with the new key
   - Updates the database

### Security Best Practices

1. **Key Management**
   - Store the encryption key in a secure secrets management system (AWS Secrets Manager, HashiCorp Vault, etc.)
   - Never commit the encryption key to version control
   - Restrict access to the encryption key to only necessary personnel

2. **Backup**
   - Always backup your encryption key securely
   - Store backups in a different location than your database backups
   - Test your key backup and recovery process

3. **Monitoring**
   - Monitor for encryption/decryption errors in your logs
   - Alert on failures to ensure data isn't being stored in plaintext due to misconfigurations

4. **Compliance**
   - Encryption at rest helps meet compliance requirements (GDPR, HIPAA, PCI-DSS, etc.)
   - Document your encryption practices for compliance audits

## Reporting Security Issues

If you discover a security vulnerability in Spotter, please email security@example.com (or report via GitHub Security Advisories if available). Please do not report security issues publicly until they have been addressed.

## Security Checklist

- [ ] Generated a secure 32-byte encryption key
- [ ] Configured `SPOTTER_SECURITY_ENCRYPTION_KEY` environment variable
- [ ] Backed up encryption key in secure location
- [ ] Verified encryption is working (check logs for "encryption initialized" message)
- [ ] Documented encryption key storage location in your operations documentation
- [ ] Restricted access to encryption key
- [ ] Tested database restore process with encrypted data
