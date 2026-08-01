# Security Policy

## Supported Versions

Only the latest release of Manifold receives security updates. Please make sure you are running the most recent version before reporting an issue.

## Reporting a Vulnerability

Manifold is a gateway that handles authentication tokens and proxies requests to backend services, so we take security reports seriously.

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, use one of the following private channels:

- **GitHub private vulnerability reporting (preferred)**: [Report a vulnerability](https://github.com/nonchan7720/manifold/security/advisories/new)

When reporting, please include as much of the following as possible:

- A description of the vulnerability and its impact
- Steps to reproduce (a minimal configuration is very helpful — with secrets removed)
- Affected version(s)
- Any suggested mitigation or fix, if you have one

## What to Expect

- We will acknowledge your report as soon as possible, typically within a few days.
- We will keep you informed of the progress toward a fix.
- Once a fix is released, we will credit you in the release notes (unless you prefer to remain anonymous).

## Scope

Reports of the following are particularly appreciated:

- Authentication / authorization bypass in the built-in OAuth 2.1 server
- Token leakage (e.g. via logs, error messages, or storage)
- SSRF bypasses in the `fileFetch` protections
- Injection or request smuggling through the OpenAPI → MCP conversion layer
