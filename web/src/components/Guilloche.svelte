<script lang="ts">
  /* Guilloché rosette — the engraved-lathe pattern from banknotes.
     Drawn as N rotated ellipses; pure decoration. */
  let {
    size = 320,
    rings = 14,
    color = 'currentColor',
    opacity = 0.14,
  }: { size?: number; rings?: number; color?: string; opacity?: number } = $props()

  const c = 100
  const paths = $derived(
    Array.from({ length: rings }, (_, i) => {
      const angle = (180 / rings) * i
      const rx = 78 - (i % 3) * 6
      const ry = 34 + (i % 5) * 4
      return { angle, rx, ry }
    }),
  )
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 200 200"
  fill="none"
  aria-hidden="true"
  style={`opacity:${opacity}`}
>
  {#each paths as p}
    <ellipse cx={c} cy={c} rx={p.rx} ry={p.ry} stroke={color} stroke-width="0.55"
      transform={`rotate(${p.angle} ${c} ${c})`} />
  {/each}
  <circle cx={c} cy={c} r="88" stroke={color} stroke-width="0.8" />
  <circle cx={c} cy={c} r="92" stroke={color} stroke-width="0.4" />
  <circle cx={c} cy={c} r="12" stroke={color} stroke-width="0.6" />
</svg>
