export function Brand({ markOnly = false, size = 20 }: { markOnly?: boolean; size?: number }) {
  return (
    <span className="flex items-center gap-2 text-[15px] font-bold tracking-tight text-ink-hi">
      <span
        className="flex items-center justify-center rounded-logo bg-brand-grad shadow-glow"
        style={{ width: size + 2, height: size + 2 }}
      >
        <svg width={size} height={size} viewBox="0 0 20 20" role="img" aria-label="Janus logo" className="text-ink-hi">
          <path d="M10 1.5 L17.5 6 V14 L10 18.5 L2.5 14 V6 Z" fill="none" stroke="currentColor" strokeWidth="1.6" />
          <path d="M10 1.5 V18.5 M10 10 L17.5 6 M10 10 L2.5 6" stroke="currentColor" strokeWidth="1.1" opacity=".55" />
          <path d="M10 1.5 L17.5 6 V14 L10 18.5 Z" fill="currentColor" opacity=".18" />
        </svg>
      </span>
      {!markOnly && <span>Janus</span>}
    </span>
  )
}
