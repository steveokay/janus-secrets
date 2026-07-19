<script lang="ts">
  /* The Janus mark: an engraved gate arch over a keyhole, line-art style. */
  let { size = 34, stroke = 'currentColor' }: { size?: number; stroke?: string } = $props()
</script>

<svg width={size} height={size} viewBox="0 0 48 48" fill="none" aria-hidden="true">
  <!-- outer plate -->
  <rect x="3" y="3" width="42" height="42" rx="2" stroke={stroke} stroke-width="1.6" />
  <rect x="6.5" y="6.5" width="35" height="35" rx="1" stroke={stroke} stroke-width="0.7" opacity="0.55" />
  <!-- gate arch -->
  <path d="M14 38 V24 a10 10 0 0 1 20 0 V38" stroke={stroke} stroke-width="1.8" />
  <path d="M17.5 38 V24.5 a6.5 6.5 0 0 1 13 0 V38" stroke={stroke} stroke-width="0.8" opacity="0.6" />
  <!-- keyhole -->
  <circle cx="24" cy="25.5" r="3.1" stroke={stroke} stroke-width="1.6" />
  <path d="M22.7 28 L21.8 33.5 h4.4 L25.3 28" stroke={stroke} stroke-width="1.6" stroke-linejoin="round" />
  <!-- hatching under the arch -->
  <path d="M10 41.5 h28" stroke={stroke} stroke-width="1.2" />
  <path d="M12 44 h24" stroke={stroke} stroke-width="0.7" opacity="0.6" />
</svg>
