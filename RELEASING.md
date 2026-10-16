# Releasing

A pushed tag `vX.Y.Z` makes release X.Y.Z. `.github/workflows/release.yml`
builds it on GitHub from that tag; nobody uploads files by hand.

## Once, on GitHub

- **Immutable releases.** Settings → General → Releases → **Enable release
  immutability**. A published release's files and tag can then never change.
  It applies only to releases published after you switch it on. Check the
  wording in GitHub's docs, "Preventing changes to your releases", as the
  settings page changes.
- **Protect the tags.** Settings → Rules → Rulesets → New tag ruleset:
  target `v*`, turn on **Restrict deletions** and **Block force pushes**, and
  set enforcement to Active.
- **Actions.** Settings → Actions → General: allow actions, and leave
  **Workflow permissions** at read-only. The workflows ask for more only
  where they need it: the release job writes the release and its
  attestations.

## Make a release

1. Set `"version"` in `extension/manifest.json` to X.Y.Z. It is the one
   place the version is written: `make dist` refuses another, the binary
   prints it for `mirrormark version`, and Chrome installs a new extension
   only when it changes.
2. Run `bin/dev run make`, `bin/dev run make check-race` and
   `bin/dev run make check-e2e`, and check `bin/dev run make dist`.
3. Commit, tag and push the tag:

       git commit -am "build: version X.Y.Z"
       git tag -a vX.Y.Z -m "mirrormark X.Y.Z"
       git push origin main vX.Y.Z

The workflow checks that the tag is `v` plus the manifest's version, runs
`make`, builds `make dist`, attests each file's build provenance, creates a
draft release, uploads every file to it, and publishes it last. Publishing
locks the release. If anything about it is wrong, release a new version:
the tag and files of a published release cannot be replaced.

## What a release holds

| File | What |
| --- | --- |
| `mirrormark_X.Y.Z_linux_amd64.tar.gz`, `_arm64` | The static binary, `LICENSE` and `README.md` |
| `mirrormark-extension_X.Y.Z.zip` | The extension's files, for Load unpacked |
| `mirrormark_X.Y.Z.xpi` | The add-on, the same bytes `install` writes into Firefox |
| `SHA256SUMS` | The SHA-256 of each file above |

There is no CRX. `mirrormark install` packs one on your computer with your
own key, so your extension id stays yours.

`make dist` is reproducible: the same commit gives the same bytes, so anyone
can rebuild a release with `bin/dev run make dist` and compare its
`SHA256SUMS`.

## Verify a download

    sha256sum -c --ignore-missing SHA256SUMS
    gh attestation verify mirrormark_X.Y.Z_linux_amd64.tar.gz --repo chriscz/mirrormark
    gh release verify vX.Y.Z --repo chriscz/mirrormark
    gh release verify-asset vX.Y.Z mirrormark_X.Y.Z_linux_amd64.tar.gz --repo chriscz/mirrormark

The first checks the files against `SHA256SUMS`. The second checks that
this repository's release workflow built the file. The last two check
GitHub's own attestation of an immutable release and that the file belongs
to it. These `gh` commands are recent; see `gh help attestation` and
`gh help release` for your version.
