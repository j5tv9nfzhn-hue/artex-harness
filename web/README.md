# web/ · 控制台

React 19 + Vite 7 + Radix Themes。构建产物写入 `server/console-app/`，由 `go:embed`
打进二进制、经 `/app/` 托管——部署仍是单文件，**运行时不需 Node**。

```bash
npm install          # 首次
npm run dev          # 开发服务器（/api 代理到 127.0.0.1:8899）
npm run build        # 产物写入 ../server/console-app/
```

> 改了 `web/` 却没重新 `npm run build`，二进制里的界面就还是旧的。CI 会用
> `git diff --exit-code server/console-app` 把这种漂移拦下来，所以**产物要和源码一起提交**。

## 数据来源

| 用途 | 端点 |
|---|---|
| 会话 / 运行列表 | `GET /api/tasks`（按 `harness_mode` 区分 HARNESS 运行与旧控制台任务） |
| 对话消息 | `GET /api/conversations/{id}/messages?since=N`（增量轮询） |
| 运行事件 | `GET /api/agui/agent?task=N&since=<seq>&follow=1`（AG-UI，断点续传 + 心跳） |
| 预算 / 子代理 / 文件 | `/api/harness/{budget,workers,files}` |

## 结构

| 文件 | 职责 |
|---|---|
| `src/App.jsx` | 视图与事件编排：任务 SSE（含重连/续传）、对话轮询、子代理面板开关、命令面板 |
| `src/Sidebar.jsx` | 会话列表（虚拟滚动）、对话与运行分区、右键菜单、折叠态 |
| `src/Composer.jsx` | 输入区：模型选择、思考深度（任务级覆盖）、上下文占用、草稿、@ 文件引用 |
| `src/SettingsView.jsx` | 设置（居中弹窗 + 弹窗内左导航，7 个分区） |
| `src/Subagents.jsx` | 子代理列表与明细（点进去看该子代理的每一步） |
| `src/lib/auth.js` | 统一鉴权请求：多枚令牌依次尝试，避免任一枚过期就整站 401 |

## 约定

- **控件只连真实端点**；后端没有的能力禁用并写明原因，不做假开关。
- 长列表一律虚拟化；每个失败态都要有原因 + 重试入口。
- 视觉全部来自库（Radix Themes / lucide / motion / recharts / sonner / react-markdown），
  不自造组件库，也不手写图标。
