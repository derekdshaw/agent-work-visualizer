import { defineConfig } from 'vite';

// The Go server owns the API and the event stream. In development, Vite serves
// the page and forwards everything else to it. changeOrigin stays off so the
// Host and Origin headers still match, which the server's checks require; the
// string shorthand would turn it on.
const server = { target: 'http://127.0.0.1:7777', changeOrigin: false };

export default defineConfig({
  build: {
    outDir: '../internal/server/web/dist',
    // The output directory is outside this project, so Vite empties it only when asked.
    emptyOutDir: true,
    chunkSizeWarningLimit: 1024,
  },
  server: {
    proxy: {
      '/api': server,
      '/events': server,
      '/gate': server,
    },
  },
});
