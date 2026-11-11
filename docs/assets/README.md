# Repository banner

`banner.png` is the 2048 × 1024 README banner. `banner.svg` is its editable source,
with the screenshot pixels embedded so it is self-contained.

The screenshots are original renders, uniformly scaled in the layout:

- `app-servers.png`: the native MyGo app rendered by `TestSnapshots`, using the
  existing `ukServers` demo fixture at 3× device scale, with the UK demo latency
  set to 96 ms. Its disconnected state,
  version, server names and latency values are fixture data, not live measurements.
- `cli-status.png`: synthetic terminal output from `cli-status.ansi`, rendered with
  Freeze v0.2.2. The account, UID, timestamps, settings and connectivity results
  are invented fixture values. No running daemon or personal configuration is read.

Regenerate the terminal image from the checked-in fixture:

```sh
freeze --config base --execute "cat docs/assets/cli-status.ansi" \
  --background '#15181c' --window --padding 24 --margin 0 \
  --font.family monospace --font.size 17 --width 1180 --output docs/assets/cli-status.png
```

After regenerating either screenshot, replace its PNG data URI in `banner.svg`
before rendering `banner.png`. Keep documentation captures entirely synthetic;
use reserved `.example` domains for subscription links.

The built-in image generation tool supplied a composition study. The final SVG
recreates its minimal charcoal/emerald layout using the original screenshots, so
generative rendering cannot change any app controls or terminal text. The study's
prompt is in `banner-prompt.txt`.

Render the source with librsvg (ImageMagick's internal SVG renderer does not render
the typography and gradients correctly):

```sh
rsvg-convert -o docs/assets/banner.png docs/assets/banner.svg
```

Generate app captures with `make screenshots`. For the banner capture, set
`tt.SetScale(3)` immediately after creating the tester in `TestSnapshots`, and set
`fixture.configs["s1"][0].Latency.LatencyMs = 96` before `newTestApp(fixture)`; restore
that temporary change afterward. Copy `dist/snapshots/servers-dark.png` here and
replace its embedded data URI in the SVG when refreshing the banner.
