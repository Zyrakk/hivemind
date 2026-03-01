/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,jsx,ts,tsx}'],
  theme: {
    extend: {
      colors: {
        hivemind: {
          bg: '#0f172a',
          card: '#1e293b',
          text: '#e2e8f0',
          muted: '#94a3b8',
          green: '#22c55e',
          yellow: '#eab308',
          blue: '#3b82f6',
          red: '#ef4444',
          gray: '#6b7280'
        }
      },
      boxShadow: {
        panel: '0 10px 30px rgba(2, 6, 23, 0.35)'
      }
    }
  },
  plugins: []
};
