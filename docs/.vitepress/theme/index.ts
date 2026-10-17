import type { Theme } from 'vitepress'
import Layout from './Layout.vue'
import Shot from './Shot.vue'
import './skeleton.css'
import './mpx.css'

export default {
  Layout,
  enhanceApp({ app }) {
    app.component('Shot', Shot)
  }
} satisfies Theme
