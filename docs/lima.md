# Linux checks in Lima VMs

`make lima-test` boots throwaway Linux guests with [Lima](https://lima-vm.io), installs
masir as the service in each, and checks the parts that only exist on Linux: the systemd
unit, its capability bound, the sandbox, the TUN, the service's ownership of its socket,
and SELinux.

It is not part of `make test`: it needs `limactl`, boots virtual machines and downloads
cloud images on the first run. Run it when a change touches the Linux unit
(`apps/masir/service_linux.go`), the daemon's privileges, TUN, the update path, or
SELinux.

## Setup

macOS:

```sh
brew install lima qemu      # qemu is for the Arch guest (see below)
```

Linux: install Lima and QEMU for the host architecture; Lima uses QEMU/KVM there.

The first run downloads each guest's cloud image (roughly 0.5–0.6 GB per distro). Lima
caches images by URL, so later runs and other instances reuse them. The Arch ARM image
does not boot under Virtualization.framework, so the `arch` guest is pinned to QEMU in
`scripts/lima/distros.conf`; on Apple Silicon that means `brew install qemu`.

## Running

```sh
make lima-test                          # ubuntu, arch, fedora
make lima-test LIMA_FLAGS="--only arch,fedora"
bash scripts/lima-test.sh --geo         # also run `masir update geo` in each guest
bash scripts/lima-test.sh --recreate    # delete and boot the guests again
bash scripts/lima-test.sh --keep        # leave them running (limactl shell masir-test-arch)
bash scripts/lima-test.sh --clean       # delete them afterwards
```

The guests are named `masir-test-<distro>` so they never collide with your own Lima
instances. Each check prints `PASS`, `FAIL` or `SKIP`; the script exits non-zero if any
check fails and ends with a summary. A copy of each guest's output is kept under
`dist/lima-logs/<distro>.log`, and the start log (image download, boot) under
`dist/lima-logs/masir-test-<distro>.start.log` — `tail -f` either one while the run is
going.

## What each guest proves

Every guest runs `scripts/lima/guest-test.sh` as root:

- `masir daemon enable --user <uid>` installs the service and the unit from the binary
  built for this host's architecture.
- The service is active, `CapabilityBoundingSet` is exactly
  `cap_chown cap_dac_override cap_net_admin`, and `ProtectSystem=full`.
- `masir status` works as the served user over the peer-checked socket.
- A stray `masir daemon run` is refused ("the masir service owns this socket") and the
  service survives it.
- Inside the unit's mount namespace `/usr/bin` and `/etc` are read-only, and
  `/usr/local/lib/masir` stays writable for updates.
- When the host has `python3`, a one-link subscription is served from the host and the
  guest does `sub add` → `use` → `connect`; a `tun` device must appear and status must
  say TUN. Without `python3` this step is a `SKIP`, not a failure.
- On Fedora (SELinux) it switches to Enforcing, checks the daemon runs in
  `unconfined_service_t`, that the audit log has no denials, and — when `semanage` is
  installed — that an `fcontext` rule exists for the service binary.
- `--geo` runs `masir update geo --force` and checks the service is still active.

## Adding a distro

Edit `scripts/lima/distros.conf`; one tab-separated line per guest:

```text
name    template            vm-type  flags
debian  template:debian     auto
```

`template:` names come from `limactl start --list-templates`. `vm-type` is `auto` or
`qemu`; `flags` may contain `selinux` or `qemu-required`. Nothing else needs changing:
the guest script detects the regular user, systemd and SELinux on its own.

## Notes and limits

- The guests run the *current checkout*: a Linux binary is cross-compiled for the host
  architecture and copied in. There is no repo mount inside the guests.
- `limactl copy` drives the guest's `rsync`; old images (the Arch one) ship a broken
  `rsync`, so the harness uses the SSH config Lima writes and `scp` instead.
- Fedora's Cloud image starts with SELinux permissive; the guest script switches it to
  enforcing. If `dnf` cannot reach the mirrors, `semanage` stays missing and that one
  check is a `SKIP`; the `chcon` label, the domain and the audit log are still checked.
- This is not a CI test: GitHub's runners have no nested virtualization for these
  guests. It is a local check, on the machine that will run the release.
- If Lima's image download dies (the Ubuntu image ended with an `unexpected EOF` here),
  the next `limactl start` starts it from zero. The image is cached by URL; to avoid that,
  fetch it yourself with `curl -L -C -` and start the instance once with
  `--set '.images=[{"location":"/path/image.qcow2","arch":"aarch64","digest":"sha256:…"}]'`.
  The instance is then reused by `make lima-test` as usual.
