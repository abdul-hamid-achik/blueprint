import { defineConfig } from 'vitepress'
import { readFileSync } from 'fs'
import { resolve, dirname } from 'path'
import { fileURLToPath } from 'url'

const __dirname = dirname(fileURLToPath(import.meta.url))

// Load the custom Blueprint TextMate grammar for syntax highlighting
const bpGrammar = JSON.parse(
  readFileSync(resolve(__dirname, 'bp-language.tmLanguage.json'), 'utf-8')
)

export default defineConfig({
  title: 'Blueprint',
  description: 'A declarative language for web services',

  // Clean URLs without .html extension
  cleanUrls: true,

  // Head metadata
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    ['meta', { name: 'theme-color', content: '#2563EB' }],
    ['meta', { name: 'og:type', content: 'website' }],
    ['meta', { name: 'og:title', content: 'Blueprint' }],
    ['meta', { name: 'og:description', content: 'A declarative language for web services' }],
    ['meta', { name: 'og:image', content: '/logo.svg' }],
  ],

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
      alt: 'Blueprint',
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
        ],
      },
      {
        text: 'Guides',
        items: [
          { text: 'Examples', link: '/examples' },
          { text: 'Generated Output', link: '/generated-output' },
        ],
      },
      {
        text: 'Contributing',
        items: [
          { text: 'Architecture', link: '/architecture' },
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
      copyright: 'Copyright 2024-present Blueprint contributors',
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
