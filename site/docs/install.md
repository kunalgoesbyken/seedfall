# Installing seedfall

This page shows every way to install seedfall and how to check that it works. If you have Go, the quickest way is `go install`. Skip the rest unless you want a package manager or a signed release.

seedfall is a single program. It also needs Go (1.25 or newer) on your machine, because it builds your code before running it. Commands that run your code need Go: `run`, `replay`, `shrink` and the matching MCP tools. Commands that only read things do not need Go: `seedfall findings`, `version`, `help` and `schema`.

**Which of these work today.** Homebrew, Scoop, the install script and Docker all install from a release made by `.github/workflows/release.yml`. The latest release, v0.2.0, is older than that workflow. It has only bare binaries, with no archives and no `checksums.txt`. Until a newer release is cut, use `go install`. The signing, SBOM, cask, manifest and image steps of the release pipeline have not run yet, so the commands below that depend on them show what the setup is meant to produce. They have not been checked against a published release.

## Quickest: go install

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
```

This needs Go 1.25 or newer. It works for every release, including those from before the automated pipeline.

## Homebrew

```sh
brew install kunalgoesbyken/tap/seedfall
```

The package depends on Go. Apple has not notarized seedfall's binaries, so macOS would normally refuse to start them. The package therefore clears the "quarantine" flag on its own binary after installing.

## Scoop (Windows)

```powershell
scoop bucket add seedfall https://github.com/kunalgoesbyken/homebrew-tap
scoop install seedfall
```

The package depends on Go.

## Install script (Linux and macOS)

```sh
curl -fsSL https://seedfall-lemon.vercel.app/install.sh | sh
```

The script picks the download for your system, checks its SHA-256 checksum (a fingerprint of the file) against the release's `checksums.txt`, and installs to `~/.local/bin`. It stops with an error if the checksum does not match. The script is `scripts/install.sh`, so you can read it first.

Settings, all optional:

- `SEEDFALL_VERSION=v0.3.0` installs a specific release.
- `SEEDFALL_INSTALL_DIR` changes where it is installed.
- `SEEDFALL_BASE_URL` downloads from a mirror.

## Docker

```sh
docker run --rm -v "$PWD:/work" ghcr.io/kunalgoesbyken/seedfall run -seeds 1-200
```

The image is built on the official `golang` image, so it can build your code without Go on your machine. Files the container writes, including `.seedfall/`, belong to root unless you pass `--user`.

## Check the install

```sh
seedfall version
seedfall doctor
```

`seedfall version` (or `--version`; add `-json` for scripts) prints the release, commit, build date, Go version and platform. A build without version-control data, such as `go install` of a tagged module, has no commit or date lines. The same version is written into `report.json`, every `.seedfall/history.jsonl` entry, failure artifacts, pinned regressions and the `seedfall verify` proof block, so a bug report can name the exact build.

`seedfall doctor` checks:

- the Go version;
- that `GOOS=wasip1` builds (`wasip1` is the WebAssembly System Interface target seedfall compiles your code for);
- that `net_overlay` patches and builds against your Go (`net_overlay` lets unmodified code use the simulated network);
- that the Go build cache and the current directory are writable;
- the free memory.

For anything wrong it prints a fix. It exits with code 1 only if a check fails. A warning, such as low memory or an unsupported `net_overlay`, still exits 0. `-json` prints the same checks, and so does the `seedfall_doctor` MCP tool. Free memory is measured on Linux and Windows and shown as `skip` elsewhere.

`seedfall help` lists every command, and `seedfall help run` prints the flags and defaults of one command. `-h` and `--help` also work. All of these exit 0.

## Verifying a release

This section is for people who want proof that a download is genuine. Each automated release publishes:

- `checksums.txt`, the list of file fingerprints;
- `checksums.txt.sigstore.json`, a Sigstore bundle: a signature, made without a long-lived key, by the release workflow;
- an SBOM (a list of the software inside) for each archive;
- a GitHub build provenance attestation, a signed record of where and how the file was built.

First verify the signature on the checksums, then check an archive against them:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/kunalgoesbyken/seedfall/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
```

Or check the provenance with the GitHub CLI:

```sh
gh attestation verify seedfall_linux_amd64.tar.gz --repo kunalgoesbyken/seedfall
```

The install script checks SHA-256 only. That catches a damaged download but not a tampered release. Use one of the commands above for that.

## Cutting a release (for maintainers)

Pushing a tag that starts with `v` runs the release workflow. It builds Linux, macOS and Windows on amd64 and arm64 with GoReleaser, signs and attests the files, publishes the GitHub release, and pushes the Homebrew cask, the Scoop manifest and the Docker image.

- The repository needs a `HOMEBREW_TAP_TOKEN` secret with write access to `kunalgoesbyken/homebrew-tap`, which holds both the `Casks/` directory and the Scoop `bucket/`.
- Release notes are grouped from commit subjects that start with `Add` or `feat`, and `Fix` or `fix`.
- A tag with a prerelease suffix, such as `v0.3.0-rc1`, is marked as a prerelease. It leaves the cask, the Scoop manifest and the `latest` image alone.
