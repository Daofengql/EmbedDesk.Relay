<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useData } from 'vitepress'

const props = defineProps<{ source: string }>()
const { isDark } = useData()
const drawing = ref('')
const error = ref(false)
let generation = 0
let disposed = false

async function render() {
  const current = ++generation
  error.value = false
  try {
    const { default: mermaid } = await import('mermaid')
    if (disposed || current !== generation) return
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: 'base',
      fontFamily: '"Segoe UI", "Microsoft YaHei", system-ui, sans-serif',
      themeVariables: isDark.value ? {
        background: '#101c24', primaryColor: '#173a45', primaryTextColor: '#deedf3',
        primaryBorderColor: '#4a9eaf', secondaryColor: '#1b3434', tertiaryColor: '#202f3b',
        lineColor: '#80adba', textColor: '#deedf3', clusterBkg: '#14232d', clusterBorder: '#345363',
        edgeLabelBackground: '#14232d'
      } : {
        primaryColor: '#e1f4f3', primaryTextColor: '#183e46', primaryBorderColor: '#47939c',
        secondaryColor: '#edf4f8', tertiaryColor: '#f1f5f9', lineColor: '#587c8a',
        textColor: '#213d47', clusterBkg: '#f5f9fb', clusterBorder: '#b7d0d8',
        edgeLabelBackground: '#f5f9fb'
      },
      flowchart: { useMaxWidth: false, padding: 18, curve: 'basis', htmlLabels: false }
    })
    const { svg } = await mermaid.render(`diagram-${crypto.randomUUID()}`, props.source)
    if (!disposed && current === generation) drawing.value = svg
  } catch {
    if (!disposed && current === generation) error.value = true
  }
}

onMounted(() => {
  render()
  watch([isDark, () => props.source], render)
})
onBeforeUnmount(() => { disposed = true; generation++ })
</script>

<template>
  <figure class="mermaid-diagram" tabindex="0" aria-label="部署结构或流程图，宽图可横向滚动查看">
    <div v-if="drawing && !error" v-html="drawing" />
    <pre v-else>{{ source }}</pre>
    <figcaption v-if="error">图表暂时无法显示，以上为流程定义。</figcaption>
  </figure>
</template>
