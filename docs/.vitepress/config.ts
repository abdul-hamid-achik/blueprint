import { defineConfig } from 'vitepress'
import { readFileSync } from 'fs'
import { resolve, dirname } from 'path'
import { fileURLToPath } from 'url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const siteUrl = 'https://blueprint-lang.dev'

// Load the custom Blueprint TextMate grammar for syntax highlighting
const bpGrammar = JSON.parse(
  readFileSync(resolve(__dirname, 'bp-language.tmLanguage.json'), 'utf-8')
)

export default defineConfig({
  lang: 'en-US',
  title: 'Blueprint',
  description: 'Describe a web service once, then compile it into a typed, runnable project you can inspect, extend, and own.',

  // Clean URLs without .html extension
  cleanUrls: true,
  lastUpdated: true,
  router: {
    // The reference pages are large; avoid fetching them before a reader asks.
    prefetchLinks: false,
  },
  sitemap: {
    hostname: siteUrl,
  },

  // Head metadata
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    ['meta', { name: 'theme-color', media: '(prefers-color-scheme: light)', content: '#F6F7F4' }],
    ['meta', { name: 'theme-color', media: '(prefers-color-scheme: dark)', content: '#11141A' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'Blueprint' }],
    ['meta', { property: 'og:image', content: `${siteUrl}/logo.svg` }],
    ['meta', { property: 'og:image:alt', content: 'Blueprint directional pipe mark' }],
    ['meta', { name: 'twitter:card', content: 'summary' }],
    ['meta', { name: 'twitter:image', content: `${siteUrl}/logo.svg` }],
  ],

  transformHead({ pageData, title, description }) {
    const path = pageData.relativePath
      .replace(/(^|\/)index\.md$/, '$1')
      .replace(/\.md$/, '')
    const canonicalUrl = `${siteUrl}/${path}`

    return [
      ['link', { rel: 'canonical', href: canonicalUrl }],
      ['meta', { property: 'og:url', content: canonicalUrl }],
      ['meta', { property: 'og:title', content: title }],
      ['meta', { property: 'og:description', content: description }],
      ['meta', { name: 'twitter:title', content: title }],
      ['meta', { name: 'twitter:description', content: description }],
    ]
  },

  // Markdown configuration
  markdown: {
    // Register the custom Blueprint language grammar
    languages: [bpGrammar],
    theme: {
      light: 'github-light',
      dark: 'github-dark',
    },
    lineNumbers: false,
  },

  themeConfig: {
    // Site logo and title
    logo: {
      light: '/logo.svg',
      dark: '/logo-dark.svg',
      // The visible site title already names this home link.
      alt: '',
    },
    siteTitle: 'Blueprint',

    // Navigation bar
    nav: [
      { text: 'Guide', link: '/getting-started' },
      { text: 'Reference', link: '/language-reference' },
      { text: 'Examples', link: '/examples' },
      {
        text: 'GitHub',
        link: 'https://github.com/abdul-hamid-achik/blueprint',
      },
    ],

    // Sidebar navigation
    sidebar: [
      {
        text: 'Introduction',
        items: [
          { text: 'What is Blueprint?', link: '/' },
          { text: 'Getting Started', link: '/getting-started' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'Language Reference', link: '/language-reference' },
          { text: 'CLI Reference', link: '/cli-reference' },
          { text: 'Error Codes', link: '/error-codes' },
          { text: 'Codegen Targets', link: '/multi-target-codegen' },
        ],
      },
      {
        text: 'Guides',
        items: [
          { text: 'Examples', link: '/examples' },
          { text: 'Generated Output', link: '/generated-output' },
          { text: 'Testing Guide', link: '/testing-guide' },
          { text: 'Deployment', link: '/deployment' },
          { text: 'LLM Generation', link: '/llm-generation' },
          { text: 'FAQ', link: '/faq' },
        ],
      },
      {
        text: 'Project',
        items: [
          { text: 'Production Readiness', link: '/production-readiness' },
          { text: 'Architecture', link: '/architecture' },
          { text: 'Package Registry (RFC)', link: '/package-registry' },
          { text: 'Roadmap', link: '/roadmap' },
          { text: 'Changelog', link: '/changelog' },
        ],
      },
    ],

    // Social links
    socialLinks: [
      { icon: 'github', link: 'https://github.com/abdul-hamid-achik/blueprint' },
    ],

    // Built-in search
    search: {
      provider: 'local',
    },

    // Footer
    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright 2024-2026 Blueprint contributors',
    },

    lastUpdated: {
      text: 'Last updated',
      formatOptions: {
        dateStyle: 'medium',
      },
    },

    // Edit link
    editLink: {
      pattern: 'https://github.com/abdul-hamid-achik/blueprint/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },

    // Outline depth (show h2 and h3 in the right sidebar)
    outline: {
      level: [2, 3],
    },
  },
})
