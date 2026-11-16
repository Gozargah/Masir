# Working in this repository

Masir is a Go workspace (`go.work`, Go 1.27.3). The desktop app is native MyGo, not a web
page. macOS arm64 is the primary development machine; Windows and Linux have their own
window implementation.

## Map

- `apps/masir` — the command-line app; `masir daemon run` is the daemon.
- `apps/masir-gui` — the tray app and its MyGo UI. This is where most UI work happens.
- `libs/masir-daemon` — daemon, routing, subscriptions, updates.
- `libs/masir-core` — client and shared core.
- `libs/masir-proto` — protobuf definitions; `make proto` regenerates.
- `patches/` — MyGo patches the GUI build applies.
- `scripts/build-gui.sh` — the desktop build for every OS and architecture.

## Fast loop

Use the smallest loop that proves the change; save the full matrix for before a commit or
a release.

```sh
cd apps/masir-gui && go test ./...                 # the module, cached
cd apps/masir-gui && go test -run TestName -v ./... # one test
make gui-local                                     # one app for this machine
make screenshots                                   # PNGs of each page in dist/snapshots
make test                                          # vet + tests for every module
make test-race                                     # the same with -race
make lima-test                                     # Linux service checks in Lima VMs
```

- `make gui-local` builds only `go env GOOS`/`GOARCH`. Use it constantly.
- `make lima-test` boots Ubuntu, Arch and Fedora (SELinux) guests with Lima and checks the
  Linux unit, its capabilities, the sandbox, the TUN and the takeover guard
  (`scripts/lima-test.sh`, `docs/lima.md`). It needs `limactl` and, for the Arch guest on
  Apple Silicon, QEMU; it is not part of `make test`. Run it when a change touches
  `apps/masir/service_linux.go`, the daemon's privileges, TUN, updates or SELinux.
- `make gui` builds all six targets and is slow; use it when touching build scripts,
  patches, platform code, or before a release.
- `make screenshots` is one headless test on this machine. `make snapshot` (singular) is
  the GoReleaser all-platform archive build and is slow; do not reach for it to check UI
  layout.
- `make gui-dev` runs from source with the same patches as `make gui`/`gui-local`
  (`scripts/dev-gui.sh`), so the hot-reload loop sees patched behavior. The headless
  tests and `go vet` still use stock MyGo: keep anything patched-only behind the
  `mygo_patched` tag.
- Cross-compile cheaply inside the module:
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`.
- Changing protobufs: `make proto`, then `make proto-lint` before committing.

## Looking at the UI without clicking

```sh
make screenshots
# or
cd apps/masir-gui
MASIR_SNAPSHOTS=/tmp/masir-snaps go test -count=1 -run TestSnapshots -v
```

This runs the headless UI test on the host only; there is no cross-platform build. Do not
confuse it with `make snapshot`, which runs GoReleaser for every release target.

`TestSnapshots` only writes images; it asserts nothing. Read the PNGs to check spacing,
centering, opacity, and theme colors. Snapshot images are also the fastest way to review a
layout change without launching the app.

For real frames and animations:

```sh
MYGO_FRAME_STATS=1   # log frames over 8 ms
MYGO_FRAME_STATS=all # log every frame
MASIR_DEMO=rules     # open the rules panel after launch
MASIR_DEMO=settings  # open settings
MASIR_DEMO=cycle     # alternate main and settings
```

Launch a built app with `MASIR_APP_NAME=MasirSmoke` so its single-instance lock, tray, and
cache do not collide with a running Masir. On macOS the executable is
`apps/masir-gui/dist/darwin-arm64/Masir.app/Contents/MacOS/Masir`. Run it for a few seconds,
capture stderr, and stop it. Demos only open pages; do not toggle daemon settings or change
subscriptions while smoke-testing unless that is the change under test.

## GUI invariants and sharp edges

- **Never do heavy work in a frame.** Parse rule JSON once (`ruleMeta`), cache searchable
  text, prefetch app icons one at a time, and keep RPC/CLI/OS checks asynchronous on
  `a.async` + `a.post`. Lists must virtualize with `ui.List`; do not build thousands of
  rows to show twenty.
- **The app bundle has one writer.** Updates of `Masir.app` belong to the MyGo updater.
  The daemon and CLI refuse a target inside `*.app` (`insideAppBundle`); a CLI inside the
  bundle tells the person to update the app. The root service's own copy is updated by the
  daemon from verified release data. Desktop imports must independently verify the
  signed component descriptor and all received bytes in private staging before execution.
- **An idle daemon reports off but saves nothing.** `--idle` keeps Xray down for the
  process; it must not write `Power=false`. The app starts the daemon in normal mode so a
  restart doesn't silently turn masir (or the tunnel) off.
- **Quitting disconnects; hiding does not.** The tray's Quit, Cmd+Q and Settings' Quit
  Masir send `SetPower(false)` as `masir disconnect` does (Always-on Proxy keeps its port
  up). Closing the window into the tray only hides it, and an update's relaunch keeps
  masir on (`upd.relaunching`).
- **Never start a daemon during an update or restart, and never beside a service.** The
  watch waits while `upd.busyUpdate()`/`restarting`, and when a service is installed it
  asks the service manager instead of spawning an unsupervised daemon.
- **A handover waits for the ports, not for the job.** `launchctl bootout` and systemd's
  stop answer once the job is gone, while the daemon they stopped still holds the proxy
  port and the TUN; the app starts the bundle daemon the moment a removal returns. So
  `masir daemon disable` stops the daemon over the socket *before* unloading it and then
  waits for the ports, the app waits again (an older CLI may not), and a daemon at boot
  treats a busy port as the daemon it replaced only while `heldPorts` says it holds the
  proxy — set at boot, cleared when boot's own apply finishes, in `BeginShutdown`, and
  after the one wait, so a port conflict from a later settings change is still reported at
  once. A pending apply must not restart Xray after `BeginShutdown` closed it (`shuttingDown`),
  or it would bind the proxy port the shutdown had just released.
  `libs/masir-daemon/ports` is the one place that knows which ports Xray binds. The
  removal must name the uid the service serves (`--user`, or sudo's `SUDO_UID` as the
  daemon's own fallback): sudo sets the real uid to 0, so `os.Getuid()` names root, which
  no daemon serves, and the port wait would refuse after the service was already gone.
- **On Unix the CLI is a client, not a supervisor, and never runs as root.** A root
  `masir` reads root's config directory and looks for root's socket, while the daemon it
  starts serves `SUDO_UID`: they never meet, and the command waits out a timeout it
  cannot win. Every command except the daemon, its update helpers and service management
  refuses to run as root (`rootRefusal`). Don't add a command to `mayRunAsRoot` to make
  one case work: either the command is an elevated helper, or it should run as the
  person. Windows is exempt: UAC keeps the same account and there is one pipe.
- **A failed start says where it looked.** `client.Start` reports the daemon's exit, the
  socket a daemon actually answers on, and the tail of the daemon's log; the daemon
  logs a timestamped `starting:` line before it resolves its user. Keep both: ten
  seconds of silence that blames an untouched log made a working daemon look broken.
- **The Linux unit is sandboxed.** `ProtectSystem=full` makes `/usr` read-only, so the
  unit lists the service's install directory in `ReadWritePaths`: an update replaces the
  service binary and the geo data there. `CapabilityBoundingSet` is exactly
  `CAP_CHOWN CAP_DAC_OVERRIDE CAP_NET_ADMIN` — verified on a systemd service in a Linux
  container (no NET_ADMIN and Xray can't create the tunnel; no CHOWN/DAC_OVERRIDE and a
  fresh `~/.config/masir` can't be created). Do not grow it without a boot test; the
  daemon refuses a takeover from an unsupervised daemon (`service.Supervised`), because a
  clean exit is not restarted. An update reconciles the definition: `masir daemon
  sync-unit` rewrites it from the service's own binary and restarts the service only when
  the file changed (the CLI and the app call it after a daemon update). A change to the
  unit must keep that path working, or existing installs never see it.
- **Signature checks fail closed.** A build without an update key refuses every release;
  `MASIR_UPDATE_ALLOW_UNSIGNED=1` is a development-only escape hatch. Updates need HTTPS
  (loopback excepted).
- **The macOS service carries its own name and icon.** It is installed as
  `/Library/PrivilegedHelperTools/masir/Masir` (Login Items uses the executable name).
  `AppIcon.rsrc`/`FinderInfo.bin` become its resource fork and FinderInfo for the Masir
  icon. Updates re-apply them after replacing the binary; never `xattr -c` the service
  binary. Legacy `/usr/local/lib/masir` services migrate through the signed offline
  helper: authenticate the legacy executable and preserve custom geo data; keep
  Homebrew parent ownership unchanged. Native tests must cover a user-owned
  `/usr/local/lib`, rather than changing it to root ownership.
  Do not remove a lowercase `masir` after copying `Masir`: the default APFS is
  case-insensitive and they are the same file.
- **The panel sizes itself from what it drew.** `heightForOpen` uses the last real
  `mainHeight`, or the closed-page height remembered in app data on the first open of a
  process, so opening never resizes a frame later. `start()` resets to the main page
  *before* it asks for the height. `settleHeight` resizes at rest when data (a status, a
  banner, the update control) changes the drawn height. Do not call `ui.NewTester` in
  production to measure: it shapes text for another engine.
- **Inputs belong inside their box.** Build a container, then create its input inside
  `box.Children(func() { ... })`. An input created just before `box.Children` is a sibling
  of the box, so its text is positioned somewhere else. This has bitten the port field and
  the add-subscription form.
- **Pinned list headers need an opaque background.** `p.subdued` is translucent; rows
  scrolling under a header use `p.listHeader`. The same applies to server group headers.
- **Warm a page in its own engine.** Do not call `ui.NewTester` in production to prewarm:
  it shapes text for another engine. A new page is built once in an unseen frame
  (`warmPage`), before the slide starts. Tests with `motion.instant = true` skip that hold
  frame.
- **Animate from the frame clock.** Springs step in `aimMotion` from `c.Now()`; use
  `c.AnimationFrame`, not timers. Height resizes are coalesced by the shell; do not resize
  from inside a frame.
- **Height follows the springs on every platform.** macOS needs
  `patches/mygo-darwin-popover-resize.patch` to stop AppKit animating its own resize. The
  Windows resize fixes are upstream since MyGo 0.7.3/0.7.4 (`#855`, `#893`, `#895`), so Windows
  carries no patch: a window's swap chain no longer stretches a stale frame, and the
  window's background shows where it grows or shrinks. If MyGo is updated, verify the macOS
  patch still applies and run `make gui-local`.
- **The panel places its own page transition.** `patches/mygo-router-page-transition.patch`
  adds `Router.PageTransition`; only builds with the `mygo_patched` tag (set by
  `scripts/build-gui.sh` and `scripts/dev-gui.sh`) compile
  `apps/masir-gui/router_transition_patched.go`, so the normal test loop uses the stock
  router and `router_transition_stock.go`. Edit the transition in `app.pageTransition`.
- **Flags are 1:1** in tiles (`Fit(ui.Cover)`, rounded corners). Use `iconTile` for flags
  and app icons so both stay centered; `TestIconTileCentersWhatItHolds` guards it.
- **Service first.** When a service is installed, use its binary and geo data; the
  bundled daemon is the no-service fallback. Matching desktop updates prepare the app,
  import independently verified daemon/geo components into private service staging,
  commit the app, then verify the service restart and relaunch. Older daemons use the
  separate CLI archive path. Never start a second bundled root daemon. Release GUI
  builds set `MASIR_SIGN_COMPONENTS=1`; ordinary source builds need no minisign secret.
- **The CLI installs itself on first launch.** macOS and Linux get a `~/.local/bin/masir`
  link; if the login shell does not already read that directory, the app appends a marked
  block (`# >>> masir command line >>>`) to `.zshrc`, the bash login file, or a fish
  `conf.d` file. Windows puts the app's `bin` directory on the user PATH. It is all
  user-level, with no admin prompt. An existing `masir` is left alone, a broken link to a
  moved Masir.app is repaired, and "Remove" records `cli-install=off` in app data so the
  next launch does not reinstall it. `MASIR_CLI_INSTALL=1` forces the install from a
  development build; `MASIR_NO_CLI_INSTALL=1` disables it.
- **Geo files are verified, not just downloaded.** `scripts/fetch-geo.sh` checks an
  existing `build/geo` file against the source sum and downloads to a temporary file; a
  killed download must never leave a truncated `.dat` that later builds reuse. Use
  `MASIR_GEO_SKIP=1` only for an offline build with known-good files.
- **Updates are checked with the person's attention, not on a timer.** Opening the panel
  (macOS) or focusing the window asks the daemon for a release at most every 10 minutes;
  the daemon answers from its daily cache when it can, and a check that finds nothing new
  is a conditional request (a 304), not a download. An ignored prompt survives checks and
  clears only when a newer release is published or the daemon restarts. Do not add a
  polling timer.
- **CLI updates include geo data.** Publish one full `masir-cli-…` archive per target,
  with the executable and both geo files. Verify the archive, preflight with staged data,
  and preserve the previous geo pair for rollback. Older binary-only updaters fall back
  to the full archive but install only its executable on the first transition. Desktop
  updates still prefer a matching delta and fall back to their full archive. See
  `docs/updates.md`.
- **Desktop updates are signed.** The public key is in `apps/masir-gui/mygo.json`; the
  matching private key is `MYGO_UPDATER_PRIVATE_KEY`, kept in `.env` or the GitHub secret.
  Generate it with `cd apps/masir-gui && go tool mygo keygen`; never commit the private
  key, keep a copy offline, and keep `.env` at mode 600: a machine that can read the key
  can sign an update for every user. The same holds for `MINISIGN_SECRET_KEY`, which
  signs the command-line and desktop checksums. `make gui`/`gui-local` produce
  `update-*.json` and the signed `masir-desktop-<os>-<version>-<arch>.tar.gz`; the release
  workflow needs both.
- **Strict list keys.** MyGo panics in tests when two rows under one parent share a key.
  When adding fixtures to the existing `c1`/`c2` configs, use distinct IDs such as `x%d`.

## Tests

- CI runs `go vet ./...` and `go test ./...` per module; the race detector runs everywhere
  except Windows. `make test` and `make test-race` mirror that locally.
- The GUI race suite takes about 15 seconds. Run it before committing GUI changes.
- The first headless frame includes engine and font initialization; do not benchmark from
  frame 1. Compare later frames, or run a second measurement after warmup.
- Use `newTestApp`, `tester`, `ukServers`, `manyRules`, and `manyConfigs`. `tester` is the
  headless `ui.NewTester`; `a.motion.instant = true` skips animation and is the default in
  those helpers.
- To test animation or height, set `a.motion.instant = false`, `tt.Frame()` to start, then
  `tt.Advance(...)`. Account for the unseen page-building frame before a page slide.
- Snapshots do not assert. If a visual rule matters, add a layout test (as with tiles) or
  state the snapshot expectation in the commit; do not rely on a golden pixel test.

## Commits

Before committing:

```sh
cd apps/masir-gui && gofmt -w . && go vet ./... && go test ./...
make test-race          # or at least the GUI module with -race
make gui-local          # when patches, platform code, or packaging changed
```

Keep `dist/`, `.mygo-patched`, generated binaries, and `.env` out of commits. Use an
imperative subject; explain the user-visible reason and any platform/patch consequence in
the body. Mention when `make gui` (all six targets) was checked for cross-platform changes.
