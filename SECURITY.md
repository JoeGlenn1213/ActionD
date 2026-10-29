# Security Policy

## Supported versions

Security fixes are applied to the latest release on the default branch (`main`). Please upgrade before reporting issues that are already fixed upstream.

## Threat model (summary)

ActionD is a **local** CI/CD engine: it runs plugin processes (often Python) against repository checkouts and exposes a REST/SSE/MCP control plane.

| Default | Meaning |
|---------|---------|
| Bind `127.0.0.1:3000` | API/console only reachable from the same machine (`ACTIOND_BIND`) |
| No `ACTIOND_TOKEN` | Unauthenticated API — **loopback trust** only |
| CORS allowlist | Browser origins limited to localhost:3000/3001 (+ `ACTIOND_CORS_ORIGIN`) |
| MCP lifecycle off | `actiond_start` / `stop` / `restart` refused unless `ACTIOND_MCP_ALLOW_LIFECYCLE=1` |

Plugins execute with the privileges of the ActionD process. Treat plugin code and repo contents as trusted for your machine.

## Hardening checklist

1. **Keep the default bind** (`127.0.0.1:3000`) unless you intentionally share on a LAN.  
2. **Never bind `0.0.0.0` (or a public interface) without `ACTIOND_TOKEN`.** Set a strong shared secret and pass it as `Authorization: Bearer …`, `X-ActionD-Token`, or `?token=` (SSE).  
3. Extend CORS only via `ACTIOND_CORS_ORIGIN` (comma-separated exact origins). Do not point wildcards at untrusted sites.  
4. Leave `ACTIOND_MCP_ALLOW_LIFECYCLE` unset unless the MCP client must start/stop the daemon.  
5. Run `actiond doctor` after install; keep LGH and ActionD updated.  
6. Prefer reverse proxy + TLS if you must expose beyond loopback; still require `ACTIOND_TOKEN`.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security-sensitive reports.

1. Prefer [GitHub Security Advisories](https://github.com/JoeGlenn1213/ActionD/security/advisories/new) (private).  
2. Or email the maintainer via the address on the [GitHub profile](https://github.com/JoeGlenn1213) with a clear subject like `ActionD security report`.

Include: affected version (`actiond version`), environment (OS/arch), reproduction steps, and impact. We will acknowledge and coordinate a fix/disclosure window.

## Related documentation

- Environment variables: [README.md § Environment variables](README.md#environment-variables)  
- Plugin trust / exec model: [docs/PLUGIN_GUIDE.md](docs/PLUGIN_GUIDE.md)
