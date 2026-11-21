import { authHeaders, authedFetch, errorText } from "./lib/auth.js";
import { useEffect, useRef, useState } from "react";
import { Box, Flex, IconButton, Select, Text, TextArea, Tooltip } from "./ui.jsx";
import { ArrowUp, FolderClosed, Plus, ShieldCheck, SlidersHorizontal, Square, Zap } from "lucide-react";

/* 输入卡片。
 *
 * 结构照参考产品的截图重建，自上而下三行：
 *   ① 芯片行：项目 + 模式（这两项都是真实状态：任务目标/项目目录、以及平台的
 *      审批模式），
 *   ② 输入区：无边框，卡片本身是唯一轮廓；占位文案与参考同形，
 *   ③ 工具条：左 = 模式段（只读/可写/全自动 → 真实对应审批规则档位），右 =
 *      模型名 + 上下文占用 + 发送按钮。
 *
 * 只有后端真的提供的选项才出现：
 *   模型      GET /api/llm/profiles + PUT /api/tasks/{id}/llm；触发面显示当前模型名
 *   思考等级  来自当前 profile 的 reasoning_effort（任务级覆盖）
 *   速度      运行时按输出字符/耗时实时估算
 *   工作目录  芯片不是展示：点开工作台，在那里绑定/切换项目目录
 *   权限      后端未暴露任务级开关 —— 芯片点开设置 → 权限与审批（拦截规则），不做假控件
 */

const EFFORT_LABEL = { low: "低", medium: "中", high: "高", minimal: "极低", xhigh: "极高" };

export default function Composer({ taskId, convId, live, budget, task, onSent, onTuned, onOpenWorkbench, onOpenPermissions }) {
  const [profiles, setProfiles] = useState([]);
  const [profileId, setProfileId] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [tps, setTps] = useState(null);
  const taRef = useRef(null);
  const [files, setFiles] = useState([]);
  const [mention, setMention] = useState(null); // {q, start} | null
  const [effort, setEffort] = useState("");     // 任务级思考深度；"" = 跟随模型
  const [efforts, setEfforts] = useState(["low", "medium", "high"]);
  const draftKey = taskId ? `artex.draft.${taskId}` : "";
  const startedAt = useRef(0);
  const chars = useRef(0);

  useEffect(() => { setEffort(task?.reasoning_effort || ""); }, [task?.reasoning_effort, taskId]);

  useEffect(() => {
    (async () => {
      try {
        const r = await authedFetch("/api/llm/profiles", { headers: authHeaders(), credentials: "include" });
        if (!r.ok) return;
        const d = await r.json();
        const list = Array.isArray(d) ? d : (d.profiles || []);
        setProfiles(list);
        const active = list.find((p) => p.is_default) || list[0];
        if (active) setProfileId(String(active.id));
      } catch { /* 选择器为空时只是不显示 */ }
    })();
  }, []);

  useEffect(() => {
    if (live === "streaming") {
      if (!startedAt.current) { startedAt.current = Date.now(); chars.current = 0; }
      const id = setInterval(() => {
        const secs = (Date.now() - startedAt.current) / 1000;
        if (secs > 0.5) setTps(chars.current / secs);
      }, 700);
      return () => clearInterval(id);
    }
    startedAt.current = 0; chars.current = 0;
    if (live === "done") return;
    setTps(null);
  }, [live]);

  const current = profiles.find((p) => String(p.id) === String(profileId));
  const pct = Math.max(0, Math.min(100, Math.round(budget?.pct || 0)));
  const pctColor = pct >= 90 ? "var(--c-status-bad)" : pct >= 70 ? "var(--c-status-awaiting)" : "var(--c-status-ok)";

  async function send() {
    const msg = text.trim();
    if (!msg || busy) return;
    setBusy(true); setErr("");
    try {
      const path = convId
        ? `/api/conversations/${encodeURIComponent(convId)}/messages`
        : taskId ? `/api/tasks/${encodeURIComponent(taskId)}/say` : "";
      if (!path) throw new Error("先新建一个对话，或在左侧选择会话");
      const r = await authedFetch(path, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ message: msg }),
      });
      if (!r.ok) throw new Error(await errorText(r));
      setText("");
      try { if (draftKey) localStorage.removeItem(draftKey); } catch { /* ignore */ }
      onSent?.();
    } catch (e) { setErr(e.message || String(e)); }
    finally { setBusy(false); }
  }

  async function changeModel(id) {
    setProfileId(id);
    if (!taskId) return;
    try {
      const r = await authedFetch(`/api/tasks/${encodeURIComponent(taskId)}/llm`, {
        method: "PUT", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ llm_profile_id: Number(id) }),
      });
      if (!r.ok) throw new Error(await errorText(r));
      onTuned?.();
    } catch (e) { setErr("切换模型失败：" + e.message); }
  }

  async function changeEffort(v) {
    setEffort(v === "inherit" ? "" : v);
    if (!taskId) return;
    try {
      const r = await authedFetch(`/api/tasks/${encodeURIComponent(taskId)}/llm`, {
        method: "PUT", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ reasoning_effort: v === "inherit" ? "" : v }),
      });
      if (!r.ok) throw new Error(await errorText(r));
      onTuned?.();
    } catch (e) { setErr("设置思考等级失败：" + e.message); }
  }

  const active = !!(taskId || convId);
  const placeholder = active ? "今天要我做什么？" : "先新建一个对话，或在左侧选择会话";

  return (
    <div className="composer-seat">
      <div className="composer">
        {/* ① 芯片行：项目 + 模式。工作目录芯片点开工作台（绑定/切换项目目录），不是摆设。 */}
        <div className="composer-chips">
          <Tooltip content="工作目录：点开工作台，在那里绑定或切换项目目录">
            <button className="chip" disabled={!taskId} onClick={() => onOpenWorkbench?.()} title={task?.project_dir || task?.goal || "当前工作区"}>
              <FolderClosed size={12} />
              {task?.project_dir ? task.project_dir.split(/[\\/]/).filter(Boolean).pop() : "工作区"}
            </button>
          </Tooltip>
          <span className="chip" title="平台管控：内置破坏性命令拦截始终生效；转人工审批由拦截规则决定">
            <ShieldCheck size={12} />
            标准模式
          </span>
        </div>

        {/* ② 输入区：无边框，卡片本身就是轮廓。 */}
        <div style={{ position: "relative" }}>
          <TextArea
            ref={taRef}
            className="composer-input"
            placeholder={placeholder}
            value={text} disabled={!active} rows={1}
            onChange={(e) => {
              const v = e.target.value;
              setText(v);
              const m = /@([\w./-]*)$/.exec(v);
              setMention(m ? { q: m[1].toLowerCase(), start: v.length - m[0].length } : null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(); }
            }}
          />
          {mention && files.length ? (
            <div className="mention-list">
              {files.filter((f) => !mention.q || f.toLowerCase().includes(mention.q)).slice(0, 8).map((f) => (
                <div key={f} className="mention-item" onClick={() => {
                  const next = text.slice(0, mention.start) + "@" + f + " ";
                  setText(next); setMention(null); taRef.current?.focus();
                }}>{f}</div>
              ))}
            </div>
          ) : null}
        </div>

        {/* ③ 工具条：左 = 只读事实（模型/思考/权限），右 = 占用 + 发送。 */}
        <div className="composer-tools">
          <div className="composer-tools__left">
            <Tooltip content="运行本任务的模型（任务级覆盖，不改全局配置）">
              <span className="tool-chip">
                <Zap size={12} />
                <Select.Root value={profileId} onValueChange={changeModel} disabled={!profiles.length || !!convId}>
                  {/* 触发面显示当前模型名：只给 placeholder 时它永远写着“选择模型”，看着像没选 */}
                  <Select.Trigger placeholder={profiles.length ? "选择模型" : "未配置模型"}>
                    {current?.name || current?.model || null}
                  </Select.Trigger>
                  <Select.Content>
                    {profiles.map((p) => (
                      <Select.Item key={p.id} value={String(p.id)}>{p.name || p.model}</Select.Item>
                    ))}
                  </Select.Content>
                </Select.Root>
              </span>
            </Tooltip>

            <Tooltip content="思考等级：只作用于这个会话，留空跟随模型配置">
              <span className="tool-chip">
                <SlidersHorizontal size={12} />
                <Select.Root value={effort || "inherit"} onValueChange={changeEffort} disabled={!taskId}>
                  <Select.Trigger>
                    {effort ? (EFFORT_LABEL[effort] || effort)
                            : ("跟随模型" + (current?.reasoning_effort ? `（${EFFORT_LABEL[current.reasoning_effort] || current.reasoning_effort}）` : ""))}
                  </Select.Trigger>
                  <Select.Content>
                    <Select.Item value="inherit">
                      跟随模型{current?.reasoning_effort ? `（${EFFORT_LABEL[current.reasoning_effort] || current.reasoning_effort}）` : ""}
                    </Select.Item>
                    {efforts.map((e) => <Select.Item key={e} value={e}>{EFFORT_LABEL[e] || e}</Select.Item>)}
                  </Select.Content>
                </Select.Root>
              </span>
            </Tooltip>

            <Tooltip content="权限与审批：点开设置，查看拦截规则与审批配置（后端没有任务级权限开关，这里不造假控件）">
              <button className="tool-chip" onClick={() => onOpenPermissions?.()}>
                <ShieldCheck size={12} />
                平台管控
              </button>
            </Tooltip>

            <Tooltip content="插入工作区文件引用（@）">
              <button className="tool-icon" aria-label="插入文件" disabled={!taskId}
                      onClick={() => { setText((t) => t + "@"); taRef.current?.focus(); }}>
                <Plus size={13} />
              </button>
            </Tooltip>
          </div>

          <div className="composer-tools__right">
            {budget && budget.window ? (
              <Tooltip content={`最近一轮输入 ${budget.last_input ?? 0} tokens · 窗口 ${budget.window} · 压缩按窗口比例触发`}>
                <span className="tool-chip tool-chip--static mono" style={{ gap: 6 }}>
                  <span className="ctx-bar"><i style={{ width: pct + "%", background: pctColor }} /></span>
                  {pct}%
                </span>
              </Tooltip>
            ) : null}
            {tps != null ? (
              <Tooltip content="估算：本轮流式输出字符数 / 已用时长">
                <span className="tool-chip tool-chip--static mono">{tps.toFixed(1)} 字/秒</span>
              </Tooltip>
            ) : null}
            <Tooltip content={busy ? "发送中…" : "发送（Enter）"}>
              <IconButton variant="solid" aria-label="发送" disabled={!text.trim() || busy} onClick={send}>
                {busy ? <Square size={12} /> : <ArrowUp size={13} />}
              </IconButton>
            </Tooltip>
          </div>
        </div>

        {err ? <div className="composer-err">{err}</div> : null}
      </div>
    </div>
  );
}
