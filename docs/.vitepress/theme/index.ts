import type { Theme } from 'vitepress'
import DefaultTheme from 'vitepress/theme-without-fonts'
import HomePage from './components/HomePage.vue'
import Layout from './Layout.vue'
import './styles/tokens.css'
import './styles/home.css'

export default {
  extends: DefaultTheme,
  Layout,
  enhanceApp({ app }) {
    app.component('HomePage', HomePage)
  },
} satisfies Theme
