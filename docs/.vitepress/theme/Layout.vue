<script setup lang="ts">
import { Analytics } from '@vercel/analytics/vue'
import DefaultTheme from 'vitepress/theme'
import { onMounted, ref } from 'vue'

const { Layout } = DefaultTheme
const isClient = ref(false)

onMounted(() => {
  isClient.value = true
})
</script>

<template>
  <Layout />
  <Analytics v-if="isClient" />
</template>
