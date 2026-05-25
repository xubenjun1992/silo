/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        primary: { DEFAULT: '#6366f1', dark: '#4f46e5' },
        accent: { DEFAULT: '#22d3ee' },
        surface: { DEFAULT: '#1e293b', light: '#334155' },
        risk: { low: '#22c55e', medium: '#f59e0b', high: '#ef4444' },
      },
    },
  },
  plugins: [],
};
