# AUR packaging — ventd

Two PKGBUILDs live here:

- `ventd-bin/` — installs the official pre-built release binary
  (amd64 / arm64). No Go toolchain required at install time; tracks
  each release tag one-for-one. This is the package most Arch users
  should install.
- `ventd/` — builds from the release source tarball with `makepkg`.
  Covers the `-bin` trust gap for users who prefer an audited source
  build. Produces a functionally identical binary to `-bin` (same
  `CGO_ENABLED=0 -trimpath` build flags as `.goreleaser.yml`).

The AUR page slugs are:

- <https://aur.archlinux.org/packages/ventd-bin>
- <https://aur.archlinux.org/packages/ventd>

## Repository layout

```
packaging/aur/
├── README.md                  (this file)
├── ventd-bin/
│   ├── PKGBUILD
│   └── ventd.install
└── ventd/
    ├── PKGBUILD
    └── ventd.install
```

`ventd.install` is a pacman install-hook script. It is identical in
both package directories by design (the two packages share post-install
behaviour) — keep the files in sync when editing.

## Publish runbook (Phoenix-only)

Publishing to `aur.archlinux.org` requires SSH push access under the
`phoenixdnb` AUR account. No Claude Code or Cowork session has these
credentials; every publish step below is a human action.

### Prerequisites

- An Arch or Arch-derivative host (or an `archlinux:base-devel`
  container) with `base-devel`, `pacman-contrib`, and `namcap`
  installed.
- SSH key registered with the AUR under the `phoenixdnb` account.
- The target tag (`vX.Y.Z`) already pushed to GitHub **and** its
  release artefacts published by goreleaser — the `-bin` PKGBUILD
  downloads the release tarball by tag.

### Per-release steps

Perform these for **both** `ventd-bin/` and `ventd/`:

1. **Bump `pkgver`.** Update the `pkgver=` line in
   `packaging/aur/ventd-bin/PKGBUILD` and
   `packaging/aur/ventd/PKGBUILD` to the new tag (without the `v`
   prefix — e.g. `pkgver=0.3.0`). Reset `pkgrel=1`.

2. **Regenerate checksums.** `SKIP` is shipped in-repo on purpose;
   real sha256sums must be inserted at publish time so downstream
   users verify the exact bytes you pushed:

   ```bash
   cd packaging/aur/ventd-bin
   updpkgsums
   cd ../ventd
   updpkgsums
   ```

   `updpkgsums` downloads the source, hashes it, and edits the
   PKGBUILD in place. Commit those edits to the AUR repo, **not**
   to `github.com/ventd/ventd` — the in-repo PKGBUILDs keep `SKIP`
   so validation runs (this repo's CI, and anyone reading the
   source) do not hit github.com/releases during review.

3. **Test the build in a clean chroot.** `makepkg -sif` alone only
   exercises the packager's own machine; a clean chroot catches
   missing `depends=` / `makedepends=` entries:

   ```bash
   # One-time setup (outside the package dir):
   sudo pacman -S --needed devtools base-devel

   cd packaging/aur/ventd-bin
   extra-x86_64-build
   # On aarch64 hosts: extra-aarch64-build

   cd ../ventd
   extra-x86_64-build
   ```

   Both must complete with exit 0. If either fails, stop — do not
   publish a broken PKGBUILD. File a tracking issue in
   `github.com/ventd/ventd` and fix the PKGBUILD in-repo first,
   then re-run the chroot build.

4. **Generate `.SRCINFO`.** The AUR requires `.SRCINFO` alongside
   the PKGBUILD and ignores missing entries silently:

   ```bash
   cd packaging/aur/ventd-bin
   makepkg --printsrcinfo > .SRCINFO
   cd ../ventd
   makepkg --printsrcinfo > .SRCINFO
   ```

   `.SRCINFO` is **not** committed to `github.com/ventd/ventd` (it
   is a derived file), only to the AUR repos.

5. **Lint with `namcap`.** Any error-level finding blocks publish.
   Warnings are judgment calls — review them before proceeding:

   ```bash
   namcap packaging/aur/ventd-bin/PKGBUILD
   namcap packaging/aur/ventd/PKGBUILD
   # And once the built packages exist:
   namcap ventd-bin-*.pkg.tar.zst
   namcap ventd-*.pkg.tar.zst
   ```

6. **Push to the AUR.** Each AUR package is its own git repo on
   `aur.archlinux.org`. Assuming you already cloned them locally:

   ```bash
   # Copy files into the local AUR checkout:
   cp packaging/aur/ventd-bin/PKGBUILD      ~/aur/ventd-bin/
   cp packaging/aur/ventd-bin/ventd.install ~/aur/ventd-bin/
   cp packaging/aur/ventd-bin/.SRCINFO      ~/aur/ventd-bin/

   cd ~/aur/ventd-bin
   git add PKGBUILD ventd.install .SRCINFO
   git commit -m "ventd-bin vX.Y.Z"
   git push origin master

   # Repeat for ventd:
   cp packaging/aur/ventd/PKGBUILD      ~/aur/ventd/
   cp packaging/aur/ventd/ventd.install ~/aur/ventd/
   cp packaging/aur/ventd/.SRCINFO      ~/aur/ventd/

   cd ~/aur/ventd
   git add PKGBUILD ventd.install .SRCINFO
   git commit -m "ventd vX.Y.Z"
   git push origin master
   ```

### Stop conditions

Stop publishing and file a `github.com/ventd/ventd` issue if any of
the following are true — do not paper over them:

- `extra-x86_64-build` or `extra-aarch64-build` fails for any reason.
  Broken AUR packages get flagged out-of-date fast and erode user
  trust.
- `namcap` emits an error-level finding on the PKGBUILD or the built
  package.
- The release tarball / source archive the PKGBUILD points at does
  not exist on github.com (e.g. the tag was pushed but goreleaser
  has not finished uploading).
- The `ventd` source PKGBUILD and the `ventd-bin` PKGBUILD disagree
  on `pkgver` — publish them in lockstep or users see temporary
  `conflicts=` failures.

## Known limitations

- **AppArmor profile attach path** — tracked upstream in
  [#122](https://github.com/ventd/ventd/issues/122). The shipped
  profile at `deploy/apparmor.d/usr.local.bin.ventd` declares its
  attach path as `/usr/local/bin/ventd`, matching the
  `.deb`/`.rpm`/tarball install location. The AUR package installs
  the binary at `/usr/bin/ventd` (Arch convention), so AppArmor
  does not confine the daemon automatically. Users who want
  enforcement before #122 lands should edit
  `/etc/apparmor.d/usr.local.bin.ventd` locally:

  ```aa
  profile ventd /usr/bin/ventd flags=(attach_disconnected) {
  ```

  Then `sudo apparmor_parser -r /etc/apparmor.d/usr.local.bin.ventd`
  and `sudo systemctl restart ventd`.

- **`sha256sums=('SKIP')` is intentional in this repo.** The real
  hashes are inserted by `updpkgsums` at publish time (step 2 above).
  Do NOT publish to the AUR with `SKIP` in place — that disables
  source verification for every user who installs the package.

## What is NOT handled here

- The v0.3 plan's "no install-path rework" still stands. This
  packaging does not alter `install.sh`, `.goreleaser.yml`, or any
  `.deb`/`.rpm` behaviour. It is purely additive.
- Nothing in `github.com/ventd/ventd` CI builds or publishes these
  PKGBUILDs automatically. That is deliberate — publishing to the
  AUR requires the account credentials described above.
