import { createRouter, createWebHistory } from 'vue-router'
import RootsView from './views/RootsView.vue'
import RootView from './views/RootView.vue'
import NodeView from './views/NodeView.vue'
import LeafView from './views/LeafView.vue'
import SeedsView from './views/SeedsView.vue'
import SeedView from './views/SeedView.vue'
import KeysView from './views/KeysView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/roots' },
    { path: '/roots', component: RootsView },
    { path: '/roots/:id', component: RootView, props: true },
    { path: '/nodes/:id', component: NodeView, props: true },
    { path: '/leaves/:id', component: LeafView, props: true },
    { path: '/seeds', component: SeedsView },
    { path: '/seeds/:id', component: SeedView, props: true },
    { path: '/keys', component: KeysView },
  ],
})
