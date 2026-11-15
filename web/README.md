# web/ · 轻量控制台

替换旧版零构建页面的 React 前端。**选型理由是能否在本机构建**：OpenBot（MIT，CopilotKit + Solid/React 全栈）
在这台 3.8GB 的机器上 dev 起不来（esbuild 扫描进程被杀）、build 被 OOM 杀掉（exit 137）；
本目录只依赖 React + Vite，`bun install` 64 包 / 45MB，`vite build` **1.3 秒**、产物 **764KB**。

数据只来自两个接口：

- `GET /api/tasks` — 任务列表
- `GET /api/agui/agent?task=N` — **AG-UI** 事件流（公开规范），投影成消息与工具卡片

产物 `server/console-app/` 由 `go:embed` 打进二进制，经 `/app/` 托管；部署仍是单文件，运行时不需 node/bun。

```bash
cd web && bun install && bun run dev     # 开发（代理 /api → :8899）
cd web && bun run build                  # 产物写入 server/console-app/
```

旧的零构建页面仍在 `/console/`，两套并存便于对比。
