/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In development the Vite server proxies /api to the Go api (docker compose
// publishes it on :8080), so the session cookie stays same-origin.
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: { '/api': process.env.API_URL ?? 'http://localhost:8080' },
  },
  test: {
    include: ['src/**/*.test.ts'],
  },
})
