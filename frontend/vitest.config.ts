import path from 'path'
import { defineConfig } from 'vitest/config'

// Unit tests run in node; render tests opt into jsdom per file via the
// @vitest-environment pragma. Kept separate from vite.config.ts to avoid
// loading the build plugins for the test run.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
})
