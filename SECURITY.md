# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub's security advisories:

https://github.com/kunalgoesbyken/seedfall/security/advisories/new

Do not open a public issue, pull request or discussion for a vulnerability. Include the seedfall version (`seedfall version`), the platform, the steps or config that reproduce it, and what an attacker gains. You will get a reply on the advisory, and the fix and advisory are published together once a patched release is out. Credit is given in the advisory unless you ask otherwise.

## Supported versions

seedfall is before 1.0. Only the latest release gets security fixes; older releases are not patched, so upgrade to get a fix.

| Version | Supported |
|---|---|
| latest release (currently v0.2.0) | yes |
| `main` | yes, fixes land here first |
| anything older | no |

## Scope

In scope:

- the `seedfall` command and its MCP server (`seedfall mcp`);
- the GitHub Action (`action.yml`, `scripts/action-install.sh`, `scripts/action-run.sh`), including how it handles the GitHub token, release downloads and checksums, and pull request comments;
- the install script (`scripts/install.sh`), release artifacts, signatures and checksums ([docs/install.md](docs/install.md));
- `seedfall report notify`, `report issue` and `report comment`, which send findings to webhooks and GitHub;
- the MCP server, skills and `seedfall hook` in this repository.

seedfall is a testing tool, not a sandbox. It runs `go build` on the host for the packages a config names, and the simulator gives guests host-backed file mappings and fixture files from the config. Treat a `seedfall.json` and the code it points at like any code you build and run. A guest reaching the host is still worth reporting, but seedfall makes no isolation guarantee for untrusted code.

Out of scope: bugs that seedfall finds in other projects. Report those to the project, through its own security process if the bug has security impact. See [CONTRIBUTING.md](CONTRIBUTING.md#writing-up-a-finding).
