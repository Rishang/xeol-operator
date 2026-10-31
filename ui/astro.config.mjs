// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';
import starlightLlmsTxt from 'starlight-llms-txt';

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
				// /llms.txt (index), /llms-full.txt and /llms-small.txt for LLM tools.
				starlightLlmsTxt({
					projectName: 'xeol-operator',
					description: 'Kubernetes operator that scans the images a cluster runs for end-of-life software (xeol) and known vulnerabilities (trivy), and exports the results as Prometheus metrics, a Grafana dashboard, a web page and MCP tools.',
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
