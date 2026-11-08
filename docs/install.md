# Installing seedfall

seedfall ships as a single binary, and it builds your nodes with Go. Replaying results, reading reports and serving MCP need only the binary; running a simulation needs Go 1.25 or newer.

**Which of these work today.** The Homebrew, Scoop, install script and Docker paths install from a release built by `.github/workflows/release.yml`. v0.2.0 predates that workflow and has only bare binaries, with no archives or `checksums.txt`, so until a newer release is cut use `go install`. The pipeline's signing, SBOM, formula, manifest and image steps have not run yet, so the commands below that depend on them are what the configuration is set up to produce, not something checked against a published release.

## Homebrew

```sh
brew install kunalgoesbyken/tap/seedfall
```

The formula depends on Go.

## Scoop (Windows)

```powershell
scoop bucket add seedfall https://github.com/kunalgoesbyken/homebrew-tap
scoop install seedfall
```

The manifest depends on Go.

## Install script (Linux and macOS)

```sh
curl -fsSL https://seedfall-lemon.vercel.app/install.sh | sh
```

It picks the archive for your OS and architecture, checks its SHA-256 against the release's `checksums.txt`, and installs to `~/.local/bin`. Set `SEEDFALL_VERSION=v0.3.0` to pin a release, `SEEDFALL_INSTALL_DIR` to change where it goes, and `SEEDFALL_BASE_URL` to download from a mirror. It stops with an error on a checksum mismatch. The script is `scripts/install.sh`; read it first if you prefer.

## Docker

```sh
docker run --rm -v "$PWD:/work" ghcr.io/kunalgoesbyken/seedfall run -seeds 1-200
```

The image is built on the official `golang` image, so it can build your nodes with no local Go. Files the container writes, including `.seedfall/`, belong to root unless you pass `--user`.

## go install

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
```

This needs Go 1.25 or newer and always works, including for releases before the automated pipeline.

## Check the install

```sh
seedfall version
seedfall doctor
```

`seedfall version` (or `--version`, with `-json` for a script) prints the release, commit, build date, Go version and platform. A build without VCS data, such as `go install` of a tagged module, has no commit or date lines. The same version goes into `report.json`, each `.seedfall/history.jsonl` entry, failure artifacts, pinned regressions and the `seedfall verify` proof block, so a bug report can name the exact build.

`seedfall doctor` checks the Go version, that `GOOS=wasip1` builds, that `net_overlay` patches and builds against the installed Go, that the Go build cache and the current directory are writable, and the free memory. It prints a fix for anything wrong and exits 1 only if a check fails; a warning, such as low memory or an unsupported `net_overlay`, leaves it at 0. `-json` prints the same checks, as does the `seedfall_doctor` MCP tool. Free memory is measured on Linux and Windows and reported as `skip` elsewhere.

`seedfall help` lists every command, and `seedfall help run` prints one command's flags and defaults. `-h` and `--help` work too and all of these exit 0.

## Verifying a release

Each automated release publishes `checksums.txt`, a Sigstore bundle `checksums.txt.sigstore.json` signed keylessly by the release workflow, an SBOM per archive, and a GitHub build provenance attestation.

Verify the signature on the checksums, then an archive against them:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/kunalgoesbyken/seedfall/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
```

Or check provenance with the GitHub CLI:

```sh
gh attestation verify seedfall_linux_amd64.tar.gz --repo kunalgoesbyken/seedfall
```

The install script checks SHA-256 only, which catches a corrupted download but not a tampered release; use one of the commands above for that.

## Cutting a release

Pushing a `v*` tag runs the release workflow, which builds linux, darwin and windows on amd64 and arm64 with GoReleaser, signs, attests, publishes the GitHub release and pushes the formula, the Scoop manifest and the image. The repository needs a `HOMEBREW_TAP_TOKEN` secret with write access to `kunalgoesbyken/homebrew-tap`, which holds both the Homebrew formula and the Scoop `bucket/`. Release notes are grouped from commit subjects starting with `Add` or `feat` and `Fix` or `fix`.
