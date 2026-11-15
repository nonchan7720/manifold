import {defineConfig} from 'vite';

// /api はバックエンド（server/main.ts）へ中継する。
export default defineConfig({
  server: {
    port: 5173,
    proxy: {'/api': {target: 'http://localhost:8787', changeOrigin: true}},
  },
});
