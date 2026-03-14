# Security Policy

## Reporting a Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

To report a security vulnerability, send an email to the maintainers with:

- A description of the vulnerability.
- Steps to reproduce or a proof-of-concept.
- The affected version(s).
- Any suggested mitigations.

You will receive a response within 5 business days. If the issue is confirmed, we will work with you to coordinate a fix and disclosure timeline.

## Supported Versions

| Version | Supported |
| ------- | --------- |
| 0.1.x   | Yes       |

## Security Design

- All upstream connections use HTTPS by default.
- `insecure_skip_verify: true` is opt-in, logged as a warning, and never set in default configs.
- No credentials are stored in default config files.
- Container images run as non-root (UID 1001).
- External URL proxying validates the host against an allowlist.
