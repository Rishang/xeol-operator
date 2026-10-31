# docs-site Specification

## Purpose
Publish the operator's documentation as a searchable website on GitHub Pages, written as MDX in the site's own source so guides can use the site's components.

## Requirements

### Requirement: Guides live in the site as MDX
Every guide (usage, Helm, configuration, air-gapped, architecture) SHALL be an MDX page under `ui/src/content/docs/` and SHALL exist only there; the repository SHALL have no second copy under `docs/`. Guides MAY use Starlight components. The README and other repository files SHALL link to guide pages on the site.

#### Scenario: Edit a guide
- **WHEN** `ui/src/content/docs/helm.mdx` is changed on `main`
- **THEN** the next site deploy shows the change on the Helm page

#### Scenario: Cross-link
- **WHEN** the usage page links to the Helm page's Install section
- **THEN** the link opens that section on the site

#### Scenario: README link
- **WHEN** a reader on GitHub follows the README's "Helm" docs link
- **THEN** it opens the Helm page on the site

### Requirement: Layout and look
The site SHALL have a grouped left sidebar, a top bar with search and a GitHub link, an "On this page" table of contents, and a light/dark toggle that follows the system setting by default, in the style of VitePress's default theme. It SHALL work at phone width.

#### Scenario: Search
- **WHEN** a user searches "air-gapped"
- **THEN** the Air-gapped page is among the results, with no external search service

#### Scenario: Dark mode
- **WHEN** the system is in dark mode and the user hasn't chosen a theme
- **THEN** the site renders dark, and the toggle switches it to light

### Requirement: Served next to the Helm repository
The site SHALL be served from `https://rishang.github.io/xeol-operator/` alongside the Helm repository's `index.yaml`, chart files and `artifacthub-repo.yml`, and every deploy SHALL include all of them.

#### Scenario: Docs-only change
- **WHEN** a push to `main` changes only `ui/src/content/docs/`
- **THEN** the site is redeployed and `index.yaml` still lists every released chart version

### Requirement: Build check
The site SHALL build with one Taskfile target, and a broken internal link SHALL fail the build.

#### Scenario: Broken link
- **WHEN** a docs page links to a missing page or heading
- **THEN** `task docs-build` fails naming the link
