import { defineConfig } from "vite";
import webExtension from "vite-plugin-web-extension";

export default defineConfig({
  plugins: [
    webExtension({
      manifest: "manifest.json",
      browser: "chrome",
      // The bridge content script (isolated world) and the native
      // document.modelContext adapter (MAIN world) are both registered
      // dynamically at runtime for the origins the edge server allows (see
      // background/contentScriptSync.ts), so neither is declared in
      // manifest.json's static content_scripts.
      additionalInputs: ["src/content/index.ts", "src/content/nativeAdapter.ts"],
    }),
  ],
});
