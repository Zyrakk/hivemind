/** @type {import('tailwindcss').Config} */
const monoStack = ['Martian Mono', 'JetBrains Mono', 'Fira Code', 'ui-monospace', 'monospace'];

export default {
  content: ['./index.html', './src/**/*.{js,jsx,ts,tsx}'],
  theme: {
    fontFamily: {
      sans: monoStack,
      mono: monoStack
    },
    extend: {
      colors: {
        hivemind: {
          bg: '#111111',
          surface: '#1c1c1c',
          border: '#2e2e2e',
          text: '#d9d9d9',
          muted: '#8c8c8c',
          dim: '#5c5c5c',
          green: '#5fba7d',
          yellow: '#d4a843',
          blue: '#6b8fc7',
          red: '#c75a5a',
          gray: '#4a4a4a'
        }
      },
      borderRadius: {
        none: '0'
      }
    }
  },
  plugins: []
};
