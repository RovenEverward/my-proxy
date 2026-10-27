import type { Theme } from 'vitepress'
import Layout from './Layout.vue'
import Shot from './Shot.vue'
import DownloadCta from './DownloadCta.vue'
import './skeleton.css'
import './mpx.css'

export default {
  Layout,
  enhanceApp({ app }) {
    app.component('Shot', Shot)
    app.component('DownloadCta', DownloadCta)
  }
} satisfies Theme
