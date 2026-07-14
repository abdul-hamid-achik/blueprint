<script setup lang="ts">
import { Analytics } from '@vercel/analytics/vue'
import DefaultTheme from 'vitepress/theme-without-fonts'
import { onMounted, ref } from 'vue'

const { Layout } = DefaultTheme
const analyticsEnabled = ref(false)

onMounted(() => {
  analyticsEnabled.value =
    import.meta.env.PROD &&
    window.location.hostname !== 'localhost' &&
    window.location.hostname !== '127.0.0.1'
})
</script>

<template>
  <Layout />
  <Analytics v-if="analyticsEnabled" />
</template>
