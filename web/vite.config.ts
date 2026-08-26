import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

/**
 * Build config — spec 05 §2.3 / plan Global Constraint #5:
 * - outDir points at the Go embed root (internal/web/static/dist).
 * - The loopback server exempts static assets from the bearer gate (spec 05
 *   §2.3), so a standard multi-file build loads without a token on assets;
 *   only /api/* + /events require the session token.
 * - base: './' keeps URLs relative (no absolute path assumptions).
 */
export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: '../internal/web/static/dist',
    emptyOutDir: true,
    target: 'es2022',
    sourcemap: false,
    cssCodeSplit: false,
    assetsInlineLimit: 4096,
    chunkSizeWarningLimit: 1600,
    rollupOptions: {
      output: {
        manualChunks: undefined,
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/__tests__/setup.ts'],
    css: false,
    include: ['src/**/*.test.{ts,tsx}'],
  },
});