# Brand assets

The kubeleash mark is an **AI agent on a leash, tethered to Kubernetes**: an ink
robot (the agent) held by a blue collar + leash that runs down to the official
Kubernetes helm (the cluster). The agent is the cluster's to restrain — the
leash and collar are Kubernetes blue (`#326CE5`); the agent is ink (`#1A2332`).

| File | Use |
|------|-----|
| `logo-mark.svg` / `.png` | Mark alone, transparent background. README header, slides, favicons. |
| `avatar.svg` / `.png` | 512×512, white rounded square + colour icon. **Org / repo avatar.** |
| `social-preview.svg` / `.png` | 1280×640 card with wordmark + tagline. **GitHub social preview.** |
| `kubernetes-icon-color.svg` | The vendored upstream Kubernetes icon (see Attribution). |

The compositions embed `kubernetes-icon-color.svg` **verbatim** and only *add*
the leash — the helm itself is the community artwork, not a redraw. The SVGs are
the source of truth; the PNGs are rendered from them. The `.mcpb` bundle icon at
`../packaging/mcpb/icon.png` is a copy of `avatar.png`.

## Attribution & trademark

`kubernetes-icon-color.svg` is the Kubernetes icon from
[cncf/artwork](https://github.com/cncf/artwork) (`projects/kubernetes/icon/color`).
The artwork files are Apache-2.0 licensed; **"Kubernetes" and the wheel logo are
trademarks of the Linux Foundation / CNCF** and their use is governed by the
[CNCF trademark guidelines](https://www.cncf.io/brand-guidelines/). This leashed
variant is an unofficial, community homage for a Kubernetes tool, not an official
Kubernetes mark. If full trademark clearance is ever needed, swap to an
abstracted wheel.

## Where to upload (web-only settings, no API)

- **Org / repo avatar** — Organization (or repo) *Settings → Profile picture* →
  upload `avatar.png`.
- **Repo social preview** — Repo *Settings → General → Social preview* → upload
  `social-preview.png` (GitHub recommends 1280×640).

## Regenerate

```bash
npm i @resvg/resvg-js   # one prebuilt dep, no system libraries
node gen.mjs            # rewrites the .svg + .png files in this directory
```

Edit colours, geometry, leash shape, or the tagline in `gen.mjs` and re-run.
