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
          bg: '#1a1a1a',
          surface: '#1f1f1f',
          border: '#2a2a2a',
          text: '#c8c8c8',
          muted: '#777777',
          dim: '#444444',
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
