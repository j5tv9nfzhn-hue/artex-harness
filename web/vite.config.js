import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 轻量前端：只依赖 React + Vite，构建产物交给 Go 侧 go:embed 托管。
// 数据来自 /api/agui/agent（AG-UI 事件流）与 /api/tasks。
export default defineConfig({
  plugins: [react()],
  base: "/app/",
  build: { outDir: "../server/console-app", emptyOutDir: true },
  server: {
    port: 3020,
    proxy: { "/api": "http://127.0.0.1:8899" },
  },
});
