# Design

## Context

- Docs today are five Markdown files in `docs/` (usage, helm, crd, air-gapped, architecture) plus the README, each starting with a `# Title` H1 and no frontmatter. They link to each other as `helm.md#install` and to images in `.github/images/`.
- `release.yml` builds and pushes `docker.io/rishang/xeol-operator:<tag>` (and `:X.Y.Z`) when a GitHub release is published. Nothing publishes the chart.
- There is no `gh-pages` branch and no Node tooling in the repo. CI (`ci.yml`) ignores `docs/**` and `**.md`.
- The repo is `Rishang/xeol-operator`, so the project Pages URL is `https://rishang.github.io/xeol-operator/` (base path `/xeol-operator/`).

## Goals / Non-Goals

**Goals:**
- One Pages site serving the docs and the Helm repo, rebuilt from scratch on every deploy (no state kept in a branch).
- The guides live once, as MDX in the site, and can use Starlight components.
- Look close to VitePress's default theme with Starlight's built-in features (Pagefind search, TOC, theme toggle).

**Non-Goals:**
- OCI charts, chart signing, versioned docs, i18n, custom domain (see proposal).
- Changing the operator or its built-in web UI.

## Decisions

### D1. Astro Starlight in `ui/`, guides moved in as MDX
`ui/` is a standard Starlight project (`astro.config.mjs`, `src/content/docs/`, `package.json` with a lockfile). Each `docs/*.md` moves with `git mv` to `ui/src/content/docs/<name>.mdx` (history follows), then:
- the H1 becomes frontmatter `title` (plus a one-line `description` for search and link previews);
- `foo.md#bar` links become site links (`/xeol-operator/foo/#bar`, through Starlight's relative `../foo/` form), and README links become the home page;
- screenshots move from `.github/images/` to `ui/src/assets/` and are imported, so Astro optimises them; the README keeps its own copies in `.github/images/`;
- where it reads better, plain Markdown becomes components: `<Tabs>` for install variants (with/without Grafana, online/mounted DB), `<Aside>` for warnings (no auth on the UI port, never `--api-key`), `<FileTree>` for volume layouts. Content and wording otherwise stay as they are.
`docs/` is deleted. The README stays the GitHub landing page (pitch, quick start, screenshots) and links to the site for each guide. The home page (`index.mdx`) is a Starlight splash page with hero, feature cards and the install snippet.
- *Alternative: keep `docs/*.md` as the source and sync into the site at build time.* Keeps GitHub-readable guides, but rules out components and needs a link-rewriting script. Dropped in favour of MDX (user decision).
- *Alternative: MDX in the site and Markdown copies in `docs/`.* Two copies drift. Rejected.

### D2. VitePress look through Starlight config and one CSS file
Starlight already has the VitePress layout (sidebar, top search, right TOC, toggle). `ui/src/styles/custom.css` sets the accent colour (the operator UI's `--steel` blue), Inter for text and a VitePress-like content width and sidebar spacing, for both themes. Sidebar groups: **Introduction** (Overview, Quick start), **Guide** (Usage, Helm, Air-gapped), **Reference** (Configuration, Architecture). Social link: GitHub. No custom components beyond the splash page.

### D3. Helm repo assembled per deploy from GitHub release assets
The release workflow, after the image job succeeds, runs `helm package helm/xeol-operator --version X.Y.Z --app-version X.Y.Z` and uploads `xeol-operator-X.Y.Z.tgz` to the GitHub release. The Pages workflow downloads the chart asset of every release (`gh release download --pattern 'xeol-operator-*.tgz'`), puts them in `dist/charts/`, and runs `helm repo index dist --url https://rishang.github.io/xeol-operator`. So `index.yaml` is regenerated each time from the releases themselves: no `gh-pages` branch, nothing to merge, and a deleted release drops its chart.
- *Alternative: `helm/chart-releaser-action`.* It commits `index.yaml` to a `gh-pages` branch, which conflicts with deploying the docs through `actions/deploy-pages` (one Pages source per repo). Rejected.
- *Alternative: index URLs pointing at the release assets.* `helm repo index --url` takes one base URL, and release assets live under per-tag paths. Serving the `.tgz` from Pages keeps one base URL. Chosen as above.

### D4. One Pages workflow, three triggers
`.github/workflows/pages.yml`: on push to `main` touching `ui/**`, `README.md`, `helm/**` or the workflow; on `workflow_call` from `release.yml` after the chart is attached; and on `workflow_dispatch`. Jobs: build (`npm ci` → `astro build` → add charts, `index.yaml`, `artifacthub-repo.yml` into `ui/dist`) → `actions/upload-pages-artifact` → `actions/deploy-pages`. Permissions: `pages: write`, `id-token: write`, `contents: read`. Concurrency group `pages`, no cancel-in-progress, so a release deploy isn't cut off.

### D5. Artifact Hub metadata
`artifacthub-repo.yml` lives in `ui/public/` (copied to the site root by Astro) with `repositoryID` (filled in after the owner adds the repo on artifacthub.io; until then the file has owners only) and `owners`. `Chart.yaml` gains annotations: `artifacthub.io/images` (`docker.io/rishang/xeol-operator`, version substituted at package time), `artifacthub.io/license: Apache-2.0`, `artifacthub.io/links` (docs site, source), `artifacthub.io/category: security`. The release workflow rewrites the image annotation's tag with `yq` before `helm package`.

### D6. Broken links fail the build
`starlight-links-validator` checks internal links and anchors during `astro build`, so a renamed heading in a guide fails `task docs-build` and the Pages workflow instead of shipping a dead link.

### D7. Taskfile targets
`docs-dev` (`astro dev`) and `docs-build` (`pnpm install --frozen-lockfile` + `astro build`). Node is a dev/CI dependency only; the operator build doesn't need it.

## Risks / Trade-offs

- [Guides no longer render on GitHub] → the README links every guide on the site; the site is the documented home. Anyone reading the source sees MDX, which is still mostly Markdown.
- [Links into `docs/` elsewhere break] → grep for `docs/` across the repo (README, AGENTS.md, `openspec/specs/`, chart `values.yaml` comments) and repoint to the site; the links validator (D6) covers links inside the site.
- [A Pages deploy downloads every release's chart] → a few KB each; fine for hundreds of releases.
- [Artifact Hub listing needs a manual step] → documented in the tasks; the repo works with `helm repo add` before that.
- [Node toolchain in a Go repo] → confined to `ui/`, pinned by `pnpm-lock.yaml` (pnpm, version in `packageManager`), built only in the Pages workflow and `task docs-*`.
- [`ui/` might be confused with the operator's `internal/api/ui/`] → `ui/README.md` says it is the docs site; the user chose `ui/`.
