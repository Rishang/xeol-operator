# Proposal

## Why

Installing xeol-operator today means cloning the repo (`helm install ./helm/xeol-operator`), and the docs are Markdown files read on GitHub. A Helm repository listed on [Artifact Hub](https://artifacthub.io) makes the chart findable and installable with `helm repo add`, and a docs site gives the guides navigation, search and a theme toggle. Both can be served for free from GitHub Pages, from the same release flow that already pushes the image to Docker Hub.

## What Changes

- **Docs site:** an [Astro Starlight](https://starlight.astro.build) site in `ui/`. The guides move from `docs/*.md` into the site as MDX (`ui/src/content/docs/*.mdx`), so they can use Starlight components (tabs for install variants, asides for warnings, cards, file trees); **`docs/` is removed** and the site is the one home for the guides. A splash home page carries the README's pitch. Layout and look follow VitePress's default theme: grouped left sidebar, top nav with search and a GitHub link, "On this page" table of contents, light/dark toggle.
- **Helm repository:** a classic Helm repo (`index.yaml` and chart `.tgz` files) served from the same GitHub Pages site, so `helm repo add xeol-operator https://rishang.github.io/xeol-operator` works and Artifact Hub can list it (`artifacthub-repo.yml`, Artifact Hub annotations in `Chart.yaml`).
- **Release flow:** publishing a GitHub release packages the chart with version and appVersion from the tag, attaches the `.tgz` to the release, and redeploys Pages. The image push to Docker Hub is unchanged. A push to `main` that touches `ui/` or the chart redeploys Pages too.
- **Docs and README:** install instructions lead with `helm repo add`; the clone install stays for development. The README stays the GitHub landing page and links to the site for every guide; links into `docs/` (README, AGENTS.md, OpenSpec specs) point to the site.
- **Taskfile:** `docs-dev` and `docs-build` targets.

No operator code, CRD field, metric or label changes.

## Non-goals

- An OCI chart on Docker Hub (considered and dropped: one chart source).
- Versioned docs, translations, or a custom domain.
- Chart signing (`.prov` files) and Artifact Hub verified-publisher setup beyond the repo metadata file.
- Moving the operator's own web UI (`internal/api/ui`) anywhere: `ui/` is the docs site only.

## Capabilities

### New Capabilities
- `helm-repository`: the published Helm chart repository: where it is served, how chart versions map to releases, and the metadata Artifact Hub reads.
- `docs-site`: the published documentation site: its source of truth, structure, look and when it is rebuilt.

### Modified Capabilities

(none: no operator behaviour changes)

## Impact

- New: `ui/` (Astro Starlight project, Node toolchain, `pnpm-lock.yaml`, pnpm), `.github/workflows/pages.yml`, `artifacthub-repo.yml` (served at the site root).
- Moved: `docs/*.md` → `ui/src/content/docs/*.mdx` (with `git mv`, so history follows), `.github/images/` screenshots used by the guides → `ui/src/assets/`.
- Changed: `.github/workflows/release.yml` (chart package and release asset, then Pages deploy), `helm/xeol-operator/Chart.yaml` (Artifact Hub annotations), `README.md`, `Taskfile.yml`, `.gitignore` (`ui/node_modules`, `ui/dist`), `AGENTS.md` (layout, release, docs location), `ci.yml` paths-ignore (`ui/**`).
- One-time manual setup by the repo owner: GitHub Pages source set to "GitHub Actions", and the repository added on artifacthub.io (its repository ID goes into `artifacthub-repo.yml`).
