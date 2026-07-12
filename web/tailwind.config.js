/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        page: 'var(--page)',
        sidebar: 'var(--sidebar)',
        topbar: 'var(--topbar)',
        card: 'var(--card)',
        elevated: 'var(--elevated)',
        line: { DEFAULT: 'var(--border)', soft: 'var(--border-soft)' },
        surface: { 1: 'var(--surface-1)', 2: 'var(--surface-2)', 3: 'var(--surface-3)' },
        ink: {
          DEFAULT: 'var(--ink)',
          hi: 'var(--ink-hi)',
          body: 'var(--ink-body)',
          mute: 'var(--ink-mute)',
          faint: 'var(--ink-faint)',
        },
        muted: 'var(--muted)',
        faint: 'var(--faint)',
        brand: {
          DEFAULT: 'var(--brand)',
          deep: 'var(--brand-deep)',
          text: 'var(--brand-text)',
          soft: 'var(--brand-soft)',
          line: 'var(--brand-line)',
        },
        success: { DEFAULT: 'var(--ok)', soft: 'var(--ok-soft)' },
        warning: { DEFAULT: 'var(--warn)', soft: 'var(--warn-soft)' },
        danger: { DEFAULT: 'var(--danger)', soft: 'var(--danger-soft)' },
        info: { DEFAULT: 'var(--info)', soft: 'var(--info-soft)' },
      },
      backgroundImage: {
        canvas: 'var(--canvas)',
        'brand-grad': 'var(--brand-grad)',
        'nav-active': 'var(--nav-active)',
        'dirty-wash': 'var(--dirty-wash)',
      },
      borderRadius: {
        DEFAULT: '8px',
        card: '12px',
        bar: '10px',
        logo: '7px',
        pill: '99px',
      },
      boxShadow: {
        card: 'var(--shadow-card)',
        pop: 'var(--shadow-pop)',
        'elev-1': 'var(--elev-1)',
        'elev-2': 'var(--elev-2)',
        glow: 'var(--glow-brand)',
        'glow-soft': 'var(--glow-brand-soft)',
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', '"Segoe UI"', 'Roboto', '"Helvetica Neue"', 'Arial', 'sans-serif'],
        mono: ['ui-monospace', '"Cascadia Code"', '"SF Mono"', 'Menlo', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
