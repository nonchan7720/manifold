import { defineConfig } from "vite";
import webExtension from "vite-plugin-web-extension";

export default defineConfig({
  plugins: [
    webExtension({
      manifest: "manifest.json",
      browser: "chrome",
      // The bridge content script is registered dynamically at runtime for the
      // origins the edge server allows (see background/contentScriptSync.ts),
      // so it isn't declared in manifest.json's static content_scripts.
      additionalInputs: ["src/content/index.ts"],
    }),
  ],
});
