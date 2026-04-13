import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Standalone Vite config for the admin dashboard.
// No WASM plugins, no cross-imports from the main client.
// Build output: admin/dist/ (embedded into Go binary via go:embed)
export default defineConfig({
  base: '/admin/',
  plugins: [react()],
  server: {
    port: 5174,
    proxy: {
      '/api/admin': 'http://localhost:8081',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
});
