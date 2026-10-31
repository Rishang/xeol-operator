# helm-repository Specification

## Purpose
Publish the xeol-operator Helm chart as a classic Helm repository on GitHub Pages, one chart version per GitHub release, with the metadata Artifact Hub needs to list it.

## Requirements

### Requirement: Helm repository on GitHub Pages
The chart SHALL be installable from `https://rishang.github.io/xeol-operator` as a classic Helm repository: an `index.yaml` at that URL listing every released chart version, and each version's `.tgz` served from the same site.

#### Scenario: Install from the repository
- **WHEN** a user runs `helm repo add xeol-operator https://rishang.github.io/xeol-operator` and `helm install xeol-operator xeol-operator/xeol-operator -n xeol-system --create-namespace`
- **THEN** the latest released chart installs without a git clone

#### Scenario: Older versions stay available
- **WHEN** releases `v0.2.0` and `v0.3.0` exist
- **THEN** `helm search repo xeol-operator --versions` lists both 0.2.0 and 0.3.0

### Requirement: Chart version follows the release
Publishing a GitHub release `vX.Y.Z` SHALL publish chart version `X.Y.Z` with `appVersion` `X.Y.Z`, so the chart's default `image.tag` is the `docker.io/rishang/xeol-operator:X.Y.Z` image the same release pushed. The chart SHALL be published only after that image push succeeds.

#### Scenario: Release v0.2.0
- **WHEN** release `v0.2.0` is published
- **THEN** chart 0.2.0 appears in `index.yaml` and `helm template` of it renders image `docker.io/rishang/xeol-operator:0.2.0`

#### Scenario: Image push fails
- **WHEN** the image build or push for a release fails
- **THEN** no chart version for that release is published

### Requirement: Artifact Hub metadata
The site root SHALL serve `artifacthub-repo.yml` (repository ID and owners), and `Chart.yaml` SHALL carry Artifact Hub annotations: the operator image (`artifacthub.io/images`), license, and links to the docs site and source.

#### Scenario: Artifact Hub reads the repository
- **WHEN** Artifact Hub fetches `https://rishang.github.io/xeol-operator/artifacthub-repo.yml` and the chart
- **THEN** it shows the chart with its image, license and docs link
