import path from 'path'
import { defineConfig } from 'vitest/config'

// Unit tests target pure logic (reducer, error mapping), so no DOM/plugins are
// needed — keep this separate from vite.config.ts to avoid loading the build
// plugins for the test run.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
})
