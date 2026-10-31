# xeol-operator docs site

The documentation site for xeol-operator, built with [Astro Starlight](https://starlight.astro.build) and published to <https://rishang.github.io/xeol-operator/> together with the Helm repository. This is not the operator's own web UI (that is `internal/api/ui/`).

- Guides: `src/content/docs/*.mdx`. Edit them here; this is their only copy.
- Look: `src/styles/custom.css`; navigation: `astro.config.mjs`.
- `task docs-dev` serves it locally, `task docs-build` builds it and fails on broken links.
