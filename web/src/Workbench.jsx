import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Badge, Box, Button, Checkbox, Code, Flex, IconButton, ScrollArea, Separator, Text, TextField, Tooltip } from "./ui.jsx";
import { ChevronRight, FileText, FolderTree, GitCompare, Play, RefreshCw, SquareTerminal, X, XCircle } from "lucide-react";
import { toast } from "./toast.jsx";
import { authedFetch, errorText } from "./lib/auth.js";

/* 工作台：右栏面板——项目目录、文件树、DIFF 审阅、终端。
 *
 * 数据来源都是任务工作区（project_dir 或 <data>/tasks/<id>）：
 *   PUT  /api/tasks/{id}/project           绑定/解绑项目目录（互斥，冲突 409 并点名占用者）
 *   GET  /api/harness/files/{id}            文件树 + 变更标记
 *   GET  /api/harness/file/{id}?path=       单文件正文（1 MiB 上限、二进制拒绝）
 *   GET  /api/harness/diff/{id}[?path=]     与基线对比的 unified diff
 *   POST /api/tasks/{id}/exec              在工作区执行一条命令（可交给 agent）
 *
 * 不做假控件：没有基线时 DIFF 明确显示 baseline=false 并说明原因；
 * 文件过大/二进制不内联，直接说明。 */

/* 错误信封的解析统一在 lib/auth.js 的 errorText：这里只需要 import，
 * 不要各面板各写一份——之前正是因为每处都自己 (await r.text()).slice()，
 * 才出现整套界面把 {"error":"..."} 这串原始 JSON 显示给操作者。 */

async function api(path, init) {
  const r = await authedFetch(path, { credentials: "include", ...(init || {}) });
  if (!r.ok) throw new Error(await errorText(r));
  return r.json();
}

const CHANGE = {
  added: { label: "A", color: "green" },
  modified: { label: "M", color: "amber" },
  deleted: { label: "D", color: "red" },
};

const TABS = [
  { key: "project", label: "项目", icon: FolderTree },
  { key: "files", label: "文件", icon: FileText },
  { key: "diff", label: "DIFF", icon: GitCompare },
  { key: "terminal", label: "终端", icon: SquareTerminal },
];

export default function Workbench({ taskId, onClose, initialTab = "files", projectDir = "", onProjectDir }) {
  const [tab, setTab] = useState(initialTab);
  return (
    <div className="subpanel">
      <Flex align="center" gap="1" px="2" py="1" style={{ borderBottom: "1px solid var(--c-divider)" }}>
        {TABS.map((t) => {
          const Icon = t.icon;
          return (
            <button key={t.key} className={"bench-tab" + (tab === t.key ? " active" : "")}
                    onClick={() => setTab(t.key)}>
              <Icon size={12} /> {t.label}
            </button>
          );
        })}
        <Box style={{ marginLeft: "auto" }} />
        <Tooltip content="关闭工作台">
          <IconButton size="1" variant="ghost" aria-label="关闭工作台" onClick={onClose}><X size={13} /></IconButton>
        </Tooltip>
      </Flex>
      {tab === "project" ? <ProjectPane taskId={taskId} projectDir={projectDir} onChanged={onProjectDir} /> : null}
      {tab === "files" ? <FilesPane taskId={taskId} /> : null}
      {tab === "diff" ? <DiffPane taskId={taskId} /> : null}
      {tab === "terminal" ? <TerminalPane taskId={taskId} /> : null}
    </div>
  );
}

/* ── 项目目录 ───────────────────────────────────────────────────────────
 * 后端一直支持 tasks.project_dir（文件树/DIFF/终端都以它为根），但控制台只在
 * 输入框旁显示一个只读 chip——没有任何设置入口。这里接上 PUT /api/tasks/{id}/project。
 *
 * 互斥由后端保证（projectGuard：一个目录只归一个活任务，冲突 409 并点名占用者），
 * 前端不复制这套判断，只把后端的拒绝原样显示出来。 */
function ProjectPane({ taskId, projectDir, onChanged }) {
  // draft 是输入框，projectDir 是**已生效**的绑定值。两者必须分开：
  // 之前共用一份 state，输入到一半点「绑定」失败时，"当前根目录"就把那个
  // 没被接受的路径显示成了事实——界面在撒谎。
  const [draft, setDraft] = useState(projectDir || "");
  const [busy, setBusy] = useState(false);
  // err 拆成两条：消息本体与状态码。原来把 r.text() 整个塞进 <Text>，
  // 用户看到的是 {"error":"目录不存在..."} 这串原始 JSON，而不是那句话。
  const [err, setErr] = useState("");
  const [conflict, setConflict] = useState(false);

  useEffect(() => { setDraft(projectDir || ""); }, [projectDir]);

  const save = useCallback(async (next) => {
    if (!taskId) return;
    setBusy(true); setErr(""); setConflict(false);
    try {
      const r = await authedFetch(`/api/tasks/${encodeURIComponent(taskId)}/project`, {
        method: "PUT",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project_dir: next }),
      });
      if (!r.ok) {
        setConflict(r.status === 409);
        throw new Error(await errorText(r));
      }
      const d = await r.json();
      setDraft(d.project_dir || "");
      toast.success(d.project_dir ? "项目目录已绑定" : "已改回任务自有目录");
      onChanged?.();
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, [taskId, onChanged]);

  if (!taskId) {
    return <Box px="3" py="3"><Text size="1" color="gray">先选择一个运行（任务）。</Text></Box>;
  }

  return (
    <ScrollArea style={{ flex: 1, minHeight: 0 }}>
      <Box px="3" py="2">
        <Text size="1" weight="medium" style={{ display: "block", marginBottom: 6 }}>当前根目录</Text>
        <Code size="1" variant="ghost" className="mono"
              style={{ display: "block", padding: "6px 8px", whiteSpace: "pre-wrap", lineHeight: 1.5 }}>
          {projectDir || "(任务自有目录 <data>/tasks/" + taskId + ")"}
        </Code>
        <Text size="1" color="gray" style={{ display: "block", marginTop: 6, lineHeight: 1.6 }}>
          文件树、DIFF、终端都以它为根。绑定项目目录后，基线不再写进你的目录，
          改放 <span className="mono">&lt;data&gt;/baselines/&lt;taskID&gt;</span>，所以 DIFF 里不会多出隐藏文件。
        </Text>

        <Separator size="4" style={{ margin: "12px 0" }} />

        <Text size="1" weight="medium" style={{ display: "block", marginBottom: 6 }}>改绑到一个绝对路径</Text>
        <TextField.Root size="1" value={draft} placeholder="/home/user/project 或 D:\\work\\target"
                        onChange={(e) => setDraft(e.target.value)}
                        onKeyDown={(e) => { if (e.key === "Enter") save(draft.trim()); }} />
        <Flex gap="2" mt="2">
          <Button size="1" variant="solid" disabled={busy} onClick={() => save(draft.trim())}>
            {busy ? "保存中…" : "绑定"}
          </Button>
          <Button size="1" variant="soft" disabled={busy || !projectDir} onClick={() => save("")}>
            改回任务自有目录
          </Button>
        </Flex>

        {err ? (
          <Box mt="2">
            <Text size="1" color="red">{err}</Text>
            {conflict ? (
              <Text size="1" color="gray" style={{ display: "block", marginTop: 4, lineHeight: 1.6 }}>
                这个目录已经被另一个活任务占用（一个目录只能归一个任务，避免两个 agent 同时改同一份文件）。
                关掉那个任务，或换一个目录。
              </Text>
            ) : null}
          </Box>
        ) : null}

        <Separator size="4" style={{ margin: "12px 0" }} />
        <Text size="1" color="gray" style={{ display: "block", lineHeight: 1.7 }}>
          路径由服务端校验：必须是绝对路径、必须存在且是目录、不能含 <span className="mono">..</span>。
          前端不做路径猜测，直接显示服务端的拒绝原因。
        </Text>
      </Box>
    </ScrollArea>
  );
}

/* ── 文件树 ───────────────────────────────────────────────────────────── */

function FilesPane({ taskId }) {
  const [tree, setTree] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [openPath, setOpenPath] = useState(null);
  const [file, setFile] = useState(null);
  const [fileErr, setFileErr] = useState("");
  const [fileBusy, setFileBusy] = useState(false);

  const load = useCallback(async () => {
    if (!taskId) return;
    setBusy(true);
    try {
      const d = await api(`/api/harness/files/${encodeURIComponent(taskId)}`);
      setTree(d); setErr("");
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, [taskId]);
  useEffect(() => { setTree(null); setOpenPath(null); setFile(null); load(); }, [load]);

  const openFile = useCallback(async (path) => {
    setOpenPath(path); setFile(null); setFileErr(""); setFileBusy(true);
    try {
      const d = await api(`/api/harness/file/${encodeURIComponent(taskId)}?path=${encodeURIComponent(path)}`);
      setFile(d);
    } catch (e) { setFileErr(e.message); }
    finally { setFileBusy(false); }
  }, [taskId]);

  const rows = useMemo(() => {
    const list = tree?.files || [];
    // Directories first at each depth, then files, stable by path.
    return list.slice().sort((a, b) => {
      if (a.dir !== b.dir) return a.dir ? -1 : 1;
      return a.path.localeCompare(b.path);
    });
  }, [tree]);

  if (openPath) {
    return (
      <Flex direction="column" height="100%" minHeight="0">
        <Flex align="center" gap="2" px="3" py="2">
          <IconButton size="1" variant="ghost" aria-label="返回文件列表" onClick={() => { setOpenPath(null); setFile(null); }}>
            <ChevronRight size={13} style={{ transform: "rotate(180deg)" }} />
          </IconButton>
          <Text size="1" weight="medium" className="mono" truncate>{openPath}</Text>
          <Box style={{ marginLeft: "auto" }} />
          <IconButton size="1" variant="ghost" aria-label="刷新文件" onClick={() => openFile(openPath)}>
            <RefreshCw size={12} className={fileBusy ? "spin" : ""} />
          </IconButton>
        </Flex>
        <Separator size="4" />
        {fileErr ? <Box px="3" py="2"><Text size="1" color="red">{fileErr}</Text></Box> : null}
        {fileBusy ? <Box px="3" py="2"><Text size="1" color="gray">读取中…</Text></Box> : null}
        {file?.too_large || file?.binary ? (
          <Box px="3" py="2"><Text size="1" color="gray">{file.note || "该文件不支持内联预览。"}</Text></Box>
        ) : null}
        {file && !file.too_large && !file.binary ? (
          <ScrollArea style={{ flex: 1, minHeight: 0 }}>
            <Code size="1" variant="ghost" className="mono"
                  style={{ display: "block", padding: "10px 12px", whiteSpace: "pre", fontSize: 11.5, lineHeight: 1.55 }}>
              {file.content || "(空文件)"}
            </Code>
          </ScrollArea>
        ) : null}
      </Flex>
    );
  }

  return (
    <Flex direction="column" height="100%" minHeight="0">
      <Flex align="center" gap="2" px="3" py="2">
        <Text size="1" color="gray">{tree ? `${(tree.files || []).length} 项` : "加载中…"}</Text>
        {tree && !tree.baseline ? <Badge size="1" color="amber">无基线</Badge> : null}
        <Box style={{ marginLeft: "auto" }} />
        <IconButton size="1" variant="ghost" aria-label="刷新文件树" onClick={load}>
          <RefreshCw size={12} className={busy ? "spin" : ""} />
        </IconButton>
      </Flex>
      <Separator size="4" />
      {err ? <Box px="3" py="2"><Text size="1" color="red">{err} <Button size="1" variant="soft" onClick={load}>重试</Button></Text></Box> : null}
      {tree && !(tree.files || []).length ? (
        <Box px="3" py="3"><Text size="1" color="gray">工作区还是空的。agent 写入文件后这里会出现，并标出相对基线的变更。</Text></Box>
      ) : null}
      <ScrollArea style={{ flex: 1, minHeight: 0 }}>
        <Box py="1">
          {rows.map((n) => {
            const depth = n.path.split("/").length - 1;
            const ch = CHANGE[n.change];
            return (
              <button key={n.path} className={"file-row" + (n.dir ? " dir" : "")}
                      style={{ paddingLeft: 10 + depth * 12 }}
                      disabled={n.dir}
                      onClick={() => { if (!n.dir) openFile(n.path); }}>
                <span className="file-name">{n.path.split("/").pop()}</span>
                {ch ? <Badge size="1" variant="soft" color={ch.color}>{ch.label}</Badge> : null}
                {!n.dir && n.size ? <span className="file-size">{fmtSize(n.size)}</span> : null}
              </button>
            );
          })}
        </Box>
      </ScrollArea>
    </Flex>
  );
}

/* ── DIFF ─────────────────────────────────────────────────────────────── */

function DiffPane({ taskId }) {
  const [set, setSet] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [sel, setSel] = useState(null);
  const [diff, setDiff] = useState(null);
  const [diffErr, setDiffErr] = useState("");

  const load = useCallback(async () => {
    if (!taskId) return;
    setBusy(true);
    try { setSet(await api(`/api/harness/diff/${encodeURIComponent(taskId)}`)); setErr(""); }
    catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, [taskId]);
  useEffect(() => { setSet(null); setSel(null); setDiff(null); load(); }, [load]);

  const open = useCallback(async (path) => {
    setSel(path); setDiff(null); setDiffErr("");
    try {
      const d = await api(`/api/harness/diff/${encodeURIComponent(taskId)}?path=${encodeURIComponent(path)}`);
      setDiff((d.files || [])[0] || null);
    } catch (e) { setDiffErr(e.message); }
  }, [taskId]);

  if (sel) {
    return (
      <Flex direction="column" height="100%" minHeight="0">
        <Flex align="center" gap="2" px="3" py="2">
          <IconButton size="1" variant="ghost" aria-label="返回变更列表" onClick={() => { setSel(null); setDiff(null); }}>
            <ChevronRight size={13} style={{ transform: "rotate(180deg)" }} />
          </IconButton>
          <Text size="1" weight="medium" className="mono" truncate>{sel}</Text>
        </Flex>
        <Separator size="4" />
        {diffErr ? <Box px="3" py="2"><Text size="1" color="red">{diffErr}</Text></Box> : null}
        {diff?.truncated ? <Box px="3" py="1"><Badge size="1" color="amber">文件过大，显示整文件替换</Badge></Box> : null}
        <ScrollArea style={{ flex: 1, minHeight: 0 }}>
          <pre className="diff-body">
            {(diff?.body || "").split("\n").map((line, i) => {
              const cls = line.startsWith("+") && !line.startsWith("+++") ? "add"
                : line.startsWith("-") && !line.startsWith("---") ? "del"
                : line.startsWith("@@") ? "hunk" : "";
              return <div key={i} className={"diff-line " + cls}>{line || " "}</div>;
            })}
          </pre>
        </ScrollArea>
      </Flex>
    );
  }

  return (
    <Flex direction="column" height="100%" minHeight="0">
      <Flex align="center" gap="2" px="3" py="2">
        <Text size="1" color="gray">{set ? `${set.changed || 0} 个变更` : "加载中…"}</Text>
        {set && !set.baseline ? <Badge size="1" color="amber">无基线</Badge> : null}
        <Box style={{ marginLeft: "auto" }} />
        <IconButton size="1" variant="ghost" aria-label="刷新 DIFF" onClick={load}>
          <RefreshCw size={12} className={busy ? "spin" : ""} />
        </IconButton>
      </Flex>
      <Separator size="4" />
      {err ? <Box px="3" py="2"><Text size="1" color="red">{err} <Button size="1" variant="soft" onClick={load}>重试</Button></Text></Box> : null}
      {set && !(set.files || []).length ? (
        <Box px="3" py="3">
          <Text size="1" color="gray">
            相对基线没有文件变更。基线在任务开始时建立；没有基线时（历史任务）下一次任务轮次会自动建立。
          </Text>
        </Box>
      ) : null}
      <ScrollArea style={{ flex: 1, minHeight: 0 }}>
        <Box py="1">
          {(set?.files || []).map((f) => (
            <button key={f.path} className="file-row" onClick={() => open(f.path)}>
              <span className="file-name mono">{f.path}</span>
              {f.additions ? <span className="diff-add">+{f.additions}</span> : null}
              {f.deletions ? <span className="diff-del">-{f.deletions}</span> : null}
              <Badge size="1" variant="soft" color={CHANGE[f.status]?.color || "gray"}>
                {CHANGE[f.status]?.label || f.status}
              </Badge>
            </button>
          ))}
        </Box>
      </ScrollArea>
    </Flex>
  );
}

/* ── 终端 ─────────────────────────────────────────────────────────────── */

function TerminalPane({ taskId }) {
  const [cmd, setCmd] = useState("");
  const [out, setOut] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [inject, setInject] = useState(false);
  const [history, setHistory] = useState([]);
  const [histIdx, setHistIdx] = useState(-1);
  const inputRef = useRef(null);

  const run = useCallback(async () => {
    const command = cmd.trim();
    if (!command) return;
    setBusy(true); setErr(""); setOut(null);
    try {
      const d = await api(`/api/tasks/${encodeURIComponent(taskId)}/exec`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ command, inject, timeout_seconds: 60 }),
      });
      setOut(d);
      setHistory((h) => [command, ...h.filter((x) => x !== command)].slice(0, 30));
      setHistIdx(-1);
      if (d.injected) toast.success("输出已交给 agent");
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); inputRef.current?.focus(); }
  }, [cmd, inject, taskId]);

  const onKey = (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); run(); return; }
    if (e.key === "ArrowUp" && history.length) {
      e.preventDefault();
      const i = Math.min(history.length - 1, histIdx + 1);
      setHistIdx(i); setCmd(history[i]);
    }
    if (e.key === "ArrowDown" && histIdx >= 0) {
      e.preventDefault();
      const i = histIdx - 1;
      setHistIdx(i); setCmd(i < 0 ? "" : history[i]);
    }
  };

  return (
    <Flex direction="column" height="100%" minHeight="0">
      <Flex align="center" gap="2" px="3" py="2">
        <Text size="1" color="gray">在任务工作区执行（bash -lc，60s 超时）</Text>
        <Box style={{ marginLeft: "auto" }} />
        <Text size="1" color="gray" as="label">
          <Flex align="center" gap="1">
            <Checkbox checked={inject} onCheckedChange={setInject} /> 交给 agent
          </Flex>
        </Text>
      </Flex>
      <Separator size="4" />
      <Box px="3" py="2">
        <TextField.Root size="1" placeholder="$ 输入命令，Enter 执行；↑↓ 历史"
                        value={cmd} onChange={(e) => setCmd(e.target.value)} onKeyDown={onKey} ref={inputRef}>
          <TextField.Slot><SquareTerminal size={12} /></TextField.Slot>
        </TextField.Root>
        <Flex gap="2" mt="2" align="center">
          <Button size="1" variant="soft" onClick={run} disabled={busy || !cmd.trim()}>
            <Play size={12} /> {busy ? "执行中…" : "执行"}
          </Button>
          {out ? (
            <Text size="1" color={out.exit_code === 0 ? "green" : "red"}>
              exit {out.exit_code} · {out.duration_ms}ms{out.injected ? " · 已交给 agent" : ""}
            </Text>
          ) : null}
          <Box style={{ marginLeft: "auto" }} />
          <Text size="1" color="gray" className="mono" truncate title={out?.cwd || ""}>{out?.cwd || ""}</Text>
        </Flex>
      </Box>
      {err ? <Box px="3" py="1"><Text size="1" color="red">{err}</Text></Box> : null}
      <ScrollArea style={{ flex: 1, minHeight: 0 }}>
        <Box px="3" py="2">
          <Code size="1" variant="ghost" className="mono"
                style={{ display: "block", whiteSpace: "pre-wrap", fontSize: 11.5, lineHeight: 1.5 }}>
            {out ? (out.output || "(无输出)") : "输出会显示在这里。"}
          </Code>
        </Box>
      </ScrollArea>
    </Flex>
  );
}

function fmtSize(n) {
  if (n < 1024) return n + "B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + "K";
  return (n / 1024 / 1024).toFixed(1) + "M";
}
