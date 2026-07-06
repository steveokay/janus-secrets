/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        page: '#F6F6FA',
        card: '#FFFFFF',
        line: { DEFAULT: '#E5E3F0', soft: '#EEECF6' },
        ink: '#211D35',
        muted: '#6E6A85',
        faint: '#9B97B0',
        brand: { DEFAULT: '#6A5CF5', deep: '#5546E0', soft: '#EFECFE', line: '#D8D2FB' },
        success: { DEFAULT: '#178A50', soft: '#E4F5EC' },
        warning: { DEFAULT: '#B45309', soft: '#FCF0DF' },
        danger: { DEFAULT: '#C92A2A', soft: '#FBE9E9' },
        info: { DEFAULT: '#2563EB', soft: '#E7EFFD' },
      },
      borderRadius: {
        DEFAULT: '8px', // controls & inputs
        card: '10px',   // cards & tables
      },
      boxShadow: {
        card: '0 1px 2px rgba(33,29,53,.05), 0 4px 16px rgba(33,29,53,.05)',
        pop: '0 4px 10px rgba(33,29,53,.08), 0 16px 40px rgba(33,29,53,.12)',
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', '"Segoe UI"', 'Roboto', '"Helvetica Neue"', 'Arial', 'sans-serif'],
        mono: ['ui-monospace', '"Cascadia Code"', '"SF Mono"', 'Menlo', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
