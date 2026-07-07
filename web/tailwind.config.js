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
        ink: 'var(--ink)',
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
      borderRadius: {
        DEFAULT: '8px',
        card: '10px',
      },
      boxShadow: {
        card: 'var(--shadow-card)',
        pop: 'var(--shadow-pop)',
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', '"Segoe UI"', 'Roboto', '"Helvetica Neue"', 'Arial', 'sans-serif'],
        mono: ['ui-monospace', '"Cascadia Code"', '"SF Mono"', 'Menlo', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
