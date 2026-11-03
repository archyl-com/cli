# Security Policy

`archyl` signs in to Archyl, stores credentials on your machine, runs as a hook
inside coding agents and executes commands on your behalf. A vulnerability here
can reach your Archyl organization and your workstation. Please report it
privately.

## Reporting a vulnerability

**Do not open a public issue, pull request or discussion for a security problem.**

- **GitHub private vulnerability reporting**: the "Report a vulnerability"
  button under this repository's Security tab. Preferred.
- **Email**: `security@archyl.com`.

Please include what an attacker gains, the smallest steps that reproduce it,
the `archyl --version` output, your OS, and whether it involves archyl.com or a
self-hosted instance.

## Scope

**In scope**

- Credential handling: the OAuth 2.1 + PKCE sign-in (`internal/auth`), token
  refresh (`internal/session`), storage in the OS keychain or the 0600 file
  (`internal/credentials`).
- Anything that executes commands or writes files: the session's actions
  (`internal/shell`), `wrap`, `guard`, `setup`, `hooks install`, `install.sh`.
- `@file` attachments reading outside the repository.
- Tenant isolation of the local cache (`internal/cache`).
- The release supply chain: checksums, cosign signatures, the GitHub Action.

**Out of scope**

- Issues that need an already compromised machine or user account.
- Vulnerabilities in the Archyl service itself: report them to the same email
  address.

## Our commitments

| Stage | Target |
| --- | --- |
| Acknowledge your report | 3 business days |
| Initial assessment and severity | 7 days |
| Fix or mitigation for critical issues | 30 days |
| Public disclosure | 90 days, or when a fix ships, whichever is sooner |

Say if you want credit in the advisory and release notes. We will not pursue
legal action against anyone acting in good faith under this policy.

## Verifying a release

Every archive is listed in `checksums.txt`, which is signed with cosign
(keyless, GitHub OIDC):

```sh
cosign verify-blob \
  --certificate checksums.txt.pem --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github\.com/archyl-com/cli/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
sha256sum --ignore-missing -c checksums.txt
```
