import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 开发期将 /api 代理到本地 Go 平台，前端代码统一用相对路径请求。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
});
