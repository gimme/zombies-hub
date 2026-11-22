// web/vite.config.ts - how Vite builds the page.
//
// `npm run build` writes the page to dist/, the folder that the server serves (server/web.go, -web-page):
//
//   index.html         the page's markup
//   assets/index.js    the page's script: src/main.ts and what it imports
//   assets/index.css   the page's sheets, which index.html links
//   the files of public/, as they are: the engine's host and its store, the service worker, the icons and the fonts
//
// The built files have fixed names, without a hash of their contents. The server has a browser revalidate each file
// when the page loads, and the service worker keeps the files by name (public/sw.js).
import { defineConfig } from 'vite';

export default defineConfig({
  build: {
    sourcemap: true,
    rolldownOptions: {
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]',
      },
    },
  },
});
