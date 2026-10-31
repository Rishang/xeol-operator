// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';
import starlightLlmsTxt from 'starlight-llms-txt';

const SITE = 'https://rishang.github.io/xeol-operator';

// The llms.txt body after the description (no headings allowed there, per llmstxt.org).
const LLMS_DETAILS = `Facts an agent needs to operate xeol-operator:

- Install: \`helm repo add xeol-operator ${SITE}\` then \`helm install xeol-operator xeol-operator/xeol-operator -n xeol-system --create-namespace\`. Optional features are Helm values: \`trivy.enabled\` (vulnerabilities), \`serviceMonitor.enabled\`, \`prometheusRule.enabled\`, \`grafanaDatasource.enabled\`, \`ui.enabled\`, \`mcp.enabled\`.
- Scan settings live in one YAML config file (no CRD): the chart renders the \`scanner.*\` values into the ConfigMap \`<release>-config\`. Fields: namespaceSelector, excludeNamespaces, excludeImages, interval, workloadKinds, workloadLabels, ignoreProducts, ignoreVulnerabilities. Edits apply live without a restart; unknown keys are rejected and the previous config stays in use.
- Pod-level settings (workers, memory, timeouts, databases, scan types, UI, MCP) are Helm values that become operator flags; changing them rolls the pod.
- Read results (read-only, no auth) on port 8080 of the Service \`<release>-metrics\`, via \`kubectl port-forward\`: \`/api/v1/status\`, \`/api/v1/namespaces\`, \`/api/v1/workloads\`, \`/api/v1/findings\` (EOL), \`/api/v1/vulnerabilities\` (with trivy), and Prometheus \`/metrics\`. Query parameters: namespace, kind, within_days, severity.
- Health: in \`/api/v1/status\`, \`images_scanned\` reaches \`images_tracked\`, \`last_sweep\` is set, \`config_error\` is empty, \`db_age_seconds\` is under a day or two.
- Accept a finding: add an \`ignoreProducts\` rule (by endoflife.date product, cycle) or an \`ignoreVulnerabilities\` rule (by CVE ID, package); both can be scoped by namespaces and a workload label selector.
- Rescan now: \`kubectl -n xeol-system exec deploy/xeol-operator -- /manager rescan [image glob...]\` (every running image without globs).
- MCP (\`mcp.enabled\`): Streamable HTTP at \`/mcp\`, read-only tools status, namespaces, workloads, findings; plus \`rescan\` only with \`mcp.allowRescan\`.
- Already EOL in PromQL: \`xeol_eol_date_seconds < time()\`; days left: \`(xeol_eol_date_seconds - time()) / 86400\`.

Pages:

- [Quick start](${SITE}/quick-start/): install and first results.
- [Usage](${SITE}/usage/): reading results, choosing what is scanned, accepting findings, alerts, private registries, rescan now, troubleshooting.
- [Configuration](${SITE}/configuration/): every config file field, ignore rules, live reload, errors, tuning xeol and trivy, a full example.
- [Helm setup](${SITE}/helm/): every chart value and feature.
- [Air-gapped installation](${SITE}/air-gapped/): mounted databases, internal mirrors.
- [Architecture](${SITE}/architecture/): how scans run, where data is stored, failure handling, security.`;

// Served from GitHub Pages next to the Helm repository (index.yaml, charts/, artifacthub-repo.yml).
export default defineConfig({
	site: 'https://rishang.github.io',
	base: '/xeol-operator/',
	integrations: [
		starlight({
			title: 'xeol-operator',
			description: 'Find end-of-life software and known vulnerabilities in the images your Kubernetes cluster runs.',
			logo: { src: './src/assets/logo.svg', alt: '' },
			favicon: '/favicon.svg',
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/Rishang/xeol-operator' }],
			editLink: { baseUrl: 'https://github.com/Rishang/xeol-operator/edit/main/ui/' },
			customCss: ['@fontsource-variable/inter', './src/styles/custom.css'],
			// VitePress-style code blocks: GitHub themes, rounded, no terminal window frame.
			expressiveCode: {
				themes: ['github-dark', 'github-light'],
				defaultProps: { frame: 'none' },
				styleOverrides: { borderRadius: '8px', borderColor: 'transparent' },
			},
			plugins: [
				// Broken internal links and anchors fail the build.
				starlightLinksValidator(),
				// /llms.txt (the entry point for AI agents), /llms-full.txt, /llms-small.txt and the topic sets
				// under /_llms-txt/. `details` is what an agent sees first: enough to act on without fetching more.
				starlightLlmsTxt({
					projectName: 'xeol-operator',
					description: 'Kubernetes operator that scans the images a cluster runs for end-of-life software (xeol) and known vulnerabilities (trivy), and exports the results as Prometheus metrics, a Grafana dashboard, a web page and MCP tools.',
					details: LLMS_DETAILS,
					customSets: [
						{ label: 'Install and use', paths: ['quick-start', 'usage'], description: 'install with Helm, check it works, read results (API, PromQL, web page, MCP), choose what is scanned, accept findings, alerts, private registries, rescan now, troubleshooting' },
						{ label: 'Configuration and Helm reference', paths: ['configuration', 'helm'], description: 'every config file field and Helm value with defaults, ignore rules, live reload and errors, tuning xeol and trivy' },
						{ label: 'Architecture and air-gapped', paths: ['architecture', 'air-gapped'], description: 'components, scan flow, storage, failure handling, security; running without internet access' },
					],
					promote: ['quick-start', 'usage', 'configuration', 'helm'],
					demote: ['architecture'],
					optionalLinks: [
						{ label: 'Source code', url: 'https://github.com/Rishang/xeol-operator', description: 'Go operator, Helm chart, these docs' },
						{ label: 'Chart values.yaml', url: 'https://github.com/Rishang/xeol-operator/blob/main/helm/xeol-operator/values.yaml', description: 'every Helm value with comments' },
					],
				}),
			],
			sidebar: [
				{
					label: 'Introduction',
					items: [
						{ label: 'Overview', link: '/' },
						{ slug: 'quick-start' },
					],
				},
				{ label: 'Guide', items: [{ slug: 'usage' }, { slug: 'helm' }, { slug: 'air-gapped' }] },
				{ label: 'Reference', items: [{ slug: 'configuration' }, { slug: 'architecture' }] },
			],
		}),
	],
});
