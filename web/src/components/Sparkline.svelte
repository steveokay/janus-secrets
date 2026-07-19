<script lang="ts">
  let {
    data,
    width = 96,
    height = 26,
    color = 'var(--archivist)',
  }: { data: number[]; width?: number; height?: number; color?: string } = $props()

  const points = $derived.by(() => {
    const max = Math.max(...data, 1)
    const stepX = width / (data.length - 1)
    return data.map((v, i) => `${(i * stepX).toFixed(1)},${(height - 2 - (v / max) * (height - 5)).toFixed(1)}`).join(' ')
  })
  const lastY = $derived(Number(points.split(' ').at(-1)?.split(',')[1] ?? 0))
</script>

<svg {width} {height} aria-hidden="true" style="display:block">
  <polyline points={points} fill="none" stroke={color} stroke-width="1.4" stroke-linejoin="round" />
  <circle cx={width} cy={lastY} r="2" fill={color} />
</svg>
