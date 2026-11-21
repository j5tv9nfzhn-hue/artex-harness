import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { formatDistanceToNowStrict, isThisWeek, isToday, isYesterday } from "date-fns";
import { zhCN } from "date-fns/locale";
import {
  Archive, Bell, Copy, Info, LogOut, MoreHorizontal, PanelLeftClose, PanelLeftOpen,
  Pencil, Pin, Plus, Search, Settings, ShieldAlert, SlidersHorizontal, FolderPlus,
} from "lucide-react";
import { Avatar, Badge, Box, Button, ContextMenu, Dialog, DropdownMenu, Flex, IconButton, Separator, BrandMark, Text, TextField, Tooltip } from "./ui.jsx";
import { authHeaders, authedFetch, errorText } from "./lib/auth.js";

/* 会话列表。
 *
 * 几何取自参考产品的截图：列宽 280px（分隔线落在 x=279）、行高 30px、6px 圆角、
 * 10px 内边距、13px 字；选中的行是 #e7e7e5 的实心板。分组标题 11px、字重 500、
 * 颜色比正文低两档。行首是一个 6px 的空心圆环——当前行才填实。
 *
 * 列表虚拟化：113 条会话也只渲染可见的十几行（@tanstack/react-virtual）。 */

const ROW_H = 30;
const HEAD_H = 28;

/** 分组：今天 / 昨天 / 本周 / 更早。 */
function groupOf(t) {
  const ts = t.created_at ? new Date(t.created_at) : null;
  if (!ts || Number.isNaN(+ts)) return "更早";
  if (isToday(ts)) return "今天";
  if (isYesterday(ts)) return "昨天";
  if (isThisWeek(ts, { weekStartsOn: 1 })) return "本周";
  return "更早";
}

/** 行尾的相对时间，参考产品用「19 分钟」这种短写法。 */
function relTime(t) {
  const ts = t.last_activity_unix ? new Date(t.last_activity_unix * 1000)
    : t.created_at ? new Date(t.created_at) : null;
  if (!ts || Number.isNaN(+ts)) return "";
  return formatDistanceToNowStrict(ts, { locale: zhCN, addSuffix: false })
    .replace(" seconds", " 秒").replace(" second", " 秒")
    .replace(" minutes", " 分钟").replace(" minute", " 分钟")
    .replace(" hours", " 小时").replace(" hour", " 小时")
    .replace(" days", " 天").replace(" day", " 天")
    .replace(" months", " 个月").replace(" month", " 个月")
    .replace(" years", " 年").replace(" year", " 年");
}

const statusColor = (s) =>
  s === "running" ? "var(--c-status-awaiting)"
    : s === "done" || s === "achieved" ? "var(--c-status-ok)"
    : s === "failed" || s === "timeout" ? "var(--c-status-bad)"
    : "var(--c-ink-4)";

export default function Sidebar({
  tasks, legacy = [], taskId, convs = [], convId, onPickConv, onPick, onNew, onPalette,
  onFilter, profiles = [], version, toast, onReload, onOpenSettings, onLogout,
}) {
  const [hover, setHover] = useState(null);
  const [about, setAbout] = useState(false);
  const [collapsed, setCollapsed] = useState(() => { try { return localStorage.getItem("artex.rail") === "collapsed"; } catch { return false; } });
  const [pending, setPending] = useState([]);
  const [notif, setNotif] = useState(false);
  const [renaming, setRenaming] = useState(null);
  const [newName, setNewName] = useState("");
  useEffect(() => { try { localStorage.setItem("artex.rail", collapsed ? "collapsed" : "open"); } catch { /* ignore */ } }, [collapsed]);
  const scrollRef = useRef(null);

  const loadPending = useCallback(async () => {
    try {
      const r = await authedFetch("/api/intercept/pending", { credentials: "include", headers: authHeaders() });
      if (r.ok) { const d = await r.json(); setPending(Array.isArray(d) ? d : (d.items || d.pending || [])); }
    } catch { /* 通知不是关键路径 */ }
  }, []);
  useEffect(() => { loadPending(); const id = setInterval(loadPending, 15000); return () => clearInterval(id); }, [loadPending]);

  async function decide(id, decision) {
    // The endpoint's vocabulary is "allowed" | "denied"; the buttons say
    // allow/deny. Sending the button wording straight through returned 400 and
    // left the run blocked.
    const body = decision === "allow" || decision === "allowed" ? "allowed" : "denied";
    try {
      const r = await authedFetch(`/api/intercept/pending/${encodeURIComponent(id)}/decide`, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ decision: body }),
      });
      if (!r.ok) throw new Error(await errorText(r));
      toast?.(decision === "allow" ? "已允许" : "已拒绝");
      loadPending();
    } catch (e) { toast?.(e.message, true); }
  }

  async function patchTask(id, body) {
    const r = await authedFetch(`/api/tasks/${encodeURIComponent(id)}`, {
      method: "PATCH", credentials: "include",
      headers: { ...authHeaders(), "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error(await errorText(r));
    onReload?.();
  }

  /* Flat list: group header, then its rows. Pinned tasks lead. */
  const flat = useMemo(() => {
    const out = [];
    // 对话（无 goal 的那条产品线）也要在侧栏有一行。
    // 之前 `convs` 只用来算高亮、从不进列表：新建对话后标签是内存态，
    // 一旦刷新，已存在的对话在界面上就再也打不开了（接口一直有返回）。
    if (convs.length) {
      out.push({ kind: "head", key: "h-conv", label: "对话", count: convs.length });
      for (const c of convs) out.push({ kind: "conv", key: "c-" + c.id, conv: c });
    }
    const sorted = [...tasks].sort((a, b) => (b.pinned ? 1 : 0) - (a.pinned ? 1 : 0));
    const groups = new Map();
    for (const t of sorted) {
      const g = t.pinned ? "置顶" : groupOf(t);
      if (!groups.has(g)) groups.set(g, []);
      groups.get(g).push(t);
    }
    const order = ["置顶", "今天", "昨天", "本周", "更早"];
    for (const g of order) {
      const list = groups.get(g);
      if (!list?.length) continue;
      out.push({ kind: "head", key: "h-" + g, label: g, count: list.length });
      for (const t of list) out.push({ kind: "task", key: "t-" + t.id, task: t });
    }
    if (legacy.length) {
      out.push({ kind: "head", key: "h-legacy", label: "旧管线任务", count: legacy.length });
      for (const t of legacy) out.push({ kind: "task", key: "t-" + t.id, task: t, legacy: true });
    }
    return out;
  }, [tasks, legacy, convs]);

  const virt = useVirtualizer({
    count: flat.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: (i) => (flat[i].kind === "head" ? HEAD_H : ROW_H),
    overscan: 12,
  });

  const activeConv = convs.find((c) => String(c.id) === String(convId));

  return (
    <aside className={"rail" + (collapsed ? " collapsed" : "")}>
      {/* Brand row: the artex-harness mark + serif wordmark. Collapsed keeps only the mark. */}
      <div className="rail-head">
        {collapsed ? (
          <Tooltip content="展开侧栏" side="right">
            <IconButton size="1" aria-label="展开侧栏" onClick={() => setCollapsed(false)}>
              <PanelLeftOpen size={15} />
            </IconButton>
          </Tooltip>
        ) : (
          <>
            <span className="rail-brand"><BrandMark size={20} />artex</span>
            <Box style={{ marginLeft: "auto", display: "flex", gap: 2 }}>
              <Tooltip content="命令面板 ⌘K">
                <IconButton size="1" aria-label="命令面板" onClick={onPalette}><Search size={14} /></IconButton>
              </Tooltip>
              <Tooltip content="收起侧栏">
                <IconButton size="1" aria-label="收起侧栏" onClick={() => setCollapsed(true)}><PanelLeftClose size={14} /></IconButton>
              </Tooltip>
            </Box>
          </>
        )}
      </div>

      {/* New conversation: the reference's circular plus chip on a full-width row. */}
      {collapsed ? (
        <Box px="2" pb="2" style={{ display: "flex", justifyContent: "center" }}>
          <Tooltip content="新建对话">
            <IconButton variant="soft" aria-label="新建对话" onClick={onNew}><Plus size={15} /></IconButton>
          </Tooltip>
        </Box>
      ) : (
        <button className="new-row" onClick={onNew}>
          <span className="plus-chip"><Plus size={13} /></span>
          新对话
        </button>
      )}

      {/* The workspace section: label + its two quiet actions, then the list. */}
      {!collapsed ? (
        <div className="group-head" style={{ paddingTop: 18 }}>
          工作区
          <span className="group-actions">
            <Tooltip content="筛选">
              <IconButton size="1" aria-label="筛选" onClick={onFilter}><SlidersHorizontal size={12} /></IconButton>
            </Tooltip>
            <Tooltip content="新建任务（带目标）">
              <IconButton size="1" aria-label="新建任务" onClick={() => onNew?.()}><FolderPlus size={12} /></IconButton>
            </Tooltip>
          </span>
        </div>
      ) : null}

      <div ref={scrollRef} className="ui-scroll" style={{ flex: 1, minHeight: 0, position: "relative" }}>
        <div style={{ height: virt.getTotalSize(), position: "relative" }}>
          {virt.getVirtualItems().map((vi) => {
            const item = flat[vi.index];
            const style = {
              position: "absolute", top: 0, left: 0, right: 0,
              transform: `translateY(${vi.start}px)`, height: vi.size,
            };
            if (item.kind === "head") {
              return (
                <div key={item.key} style={style} className="group-head">
                  {item.label}
                  <span style={{ marginLeft: "auto", fontSize: 10, color: "var(--c-ink-4)" }}>{item.count}</span>
                </div>
              );
            }
            const t = item.task;
            if (item.kind === "conv") {
              const c = item.conv;
              const convActive = String(c.id) === String(convId);
              return (
                <div key={item.key} style={style}
                     className={"row" + (convActive ? " active" : "")}
                     role="button" tabIndex={0}
                     aria-label={collapsed ? (c.title || "对话 " + c.id) : undefined}
                     title={collapsed ? (c.title || "对话 " + c.id) : undefined}
                     onClick={() => onPickConv?.(String(c.id))}
                     onKeyDown={(e) => { if (e.key === "Enter") onPickConv?.(String(c.id)); }}>
                  <span className="dot" style={{ color: c.running ? "var(--c-status-running)" : "var(--c-ink-4)" }} />
                  {collapsed ? null : <span className="row-title">{c.title || "新对话"}</span>}
                  {collapsed ? null : (
                    <span className="row-tail">
                      <span className="row-time">{relTime({ created_at: c.updated_at || c.created_at })}</span>
                    </span>
                  )}
                </div>
              );
            }
            const active = String(t.id) === String(taskId);
            const isHover = hover === t.id;
            return (
              <ContextMenu.Root key={item.key}>
                <ContextMenu.Trigger>
                  <div style={style}
                       className={"row" + (active ? " active" : "")}
                       title={collapsed ? (t.name || t.goal || t.description || "task " + t.id) : undefined}
                       aria-label={collapsed ? (t.name || t.goal || t.description || "task " + t.id) : undefined}
                       onMouseEnter={() => setHover(t.id)}
                       onMouseLeave={() => setHover((h) => (h === t.id ? null : h))}
                       onClick={() => onPick(String(t.id))}
                       role="button" tabIndex={0}
                       onKeyDown={(e) => { if (e.key === "Enter") onPick(String(t.id)); }}>
                    <span className="dot" style={{ color: statusColor(t.status) }} />
                    {collapsed ? null : (
                      <span className="row-title">{t.name || t.goal || t.description || "task " + t.id}</span>
                    )}
                    {collapsed ? null : (
                      <span className="row-tail">
                        {isHover || active ? (
                          <DropdownMenu.Root>
                            <DropdownMenu.Trigger>
                              <IconButton size="1" aria-label="会话操作" onClick={(e) => e.stopPropagation()}>
                                <MoreHorizontal size={13} />
                              </IconButton>
                            </DropdownMenu.Trigger>
                            <DropdownMenu.Content align="end">
                              <DropdownMenu.Item onSelect={() => onPick(String(t.id))}>打开</DropdownMenu.Item>
                              <DropdownMenu.Item onSelect={() => navigator.clipboard?.writeText(String(t.id))}>
                                <Copy size={12} /> 复制 ID #{t.id}
                              </DropdownMenu.Item>
                              <DropdownMenu.Item onSelect={() => patchTask(t.id, { pinned: !t.pinned }).catch((e) => toast?.(e.message, true))}>
                                <Pin size={12} /> {t.pinned ? "取消置顶" : "置顶"}
                              </DropdownMenu.Item>
                              <DropdownMenu.Item onSelect={() => { setRenaming(t); setNewName(t.name || ""); }}>
                                <Pencil size={12} /> 重命名
                              </DropdownMenu.Item>
                              <DropdownMenu.Separator />
                              <DropdownMenu.Item onSelect={() => authedFetch(`/api/tasks/${encodeURIComponent(t.id)}/archive`, { method: "POST", credentials: "include", headers: authHeaders() }).then(() => onReload?.()).catch((e) => toast?.(e.message, true))}>
                                <Archive size={12} /> 归档
                              </DropdownMenu.Item>
                              <DropdownMenu.Item color="red"
                                                 onSelect={() => authedFetch(`/api/tasks/${encodeURIComponent(t.id)}`, { method: "DELETE", credentials: "include", headers: authHeaders() }).then(() => onReload?.()).catch((e) => toast?.(e.message, true))}>
                                删除
                              </DropdownMenu.Item>
                            </DropdownMenu.Content>
                          </DropdownMenu.Root>
                        ) : null}
                        {(() => {
                          const f = t.findings || {};
                          const total = (f.critical || 0) + (f.high || 0) + (f.medium || 0) + (f.low || 0);
                          if (!total) return null;
                          return (
                            <span className="row-time"
                                  title={`发现 ${total} 条（严重 ${f.critical || 0} / 高 ${f.high || 0} / 中 ${f.medium || 0} / 低 ${f.low || 0}）`}
                                  style={{ color: f.critical ? "var(--red-9)" : "var(--c-ink-4)" }}>
                              发现 {total}
                            </span>
                          );
                        })()}
                        <span className="row-time">{relTime(t)}</span>
                      </span>
                    )}
                  </div>
                </ContextMenu.Trigger>
                <ContextMenu.Content>
                  <ContextMenu.Item onSelect={() => onPick(String(t.id))}>打开</ContextMenu.Item>
                  <ContextMenu.Item onSelect={() => navigator.clipboard?.writeText(String(t.id))}>复制 ID</ContextMenu.Item>
                  <ContextMenu.Item onSelect={() => patchTask(t.id, { pinned: !t.pinned }).catch((e) => toast?.(e.message, true))}>
                    {t.pinned ? "取消置顶" : "置顶"}
                  </ContextMenu.Item>
                  <ContextMenu.Separator />
                  <ContextMenu.Item color="red"
                                   onSelect={() => authedFetch(`/api/tasks/${encodeURIComponent(t.id)}`, { method: "DELETE", credentials: "include", headers: authHeaders() }).then(() => onReload?.()).catch((e) => toast?.(e.message, true))}>
                    删除
                  </ContextMenu.Item>
                </ContextMenu.Content>
              </ContextMenu.Root>
            );
          })}
        </div>
      </div>

      {/* Foot: the account row, and the pending-approval bell beside it. */}
      <div className="rail-foot">
        {collapsed ? (
          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 6, padding: "8px 0" }}>
            <Tooltip content={pending.length ? `${pending.length} 个待审批` : "没有待审批"} side="right">
              <IconButton size="1" aria-label="通知" onClick={() => setNotif(true)}>
                <span style={{ position: "relative", display: "inline-flex" }}>
                  <Bell size={14} />
                  {pending.length ? <span className="notif-dot">{pending.length > 9 ? "9+" : pending.length}</span> : null}
                </span>
              </IconButton>
            </Tooltip>
            <Tooltip content="设置" side="right">
              <IconButton size="1" aria-label="设置" onClick={() => onOpenSettings?.()}><Settings size={14} /></IconButton>
            </Tooltip>
            <button className="avatar-btn" aria-label="账户与菜单" onClick={() => setAbout(true)}>
              <Avatar fallback="A" />
            </button>
          </div>
        ) : (
          <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "8px 10px" }}>
            <button className="avatar-btn" aria-label="账户与菜单" onClick={() => setAbout(true)}>
              <Avatar fallback="A" />
            </button>
            <Box style={{ minWidth: 0, flex: 1 }}>
              <div style={{ fontSize: 12, fontWeight: 500, lineHeight: 1.2 }}>artex-harness</div>
              <div style={{ fontSize: 10, color: "var(--c-ink-4)", lineHeight: 1.3 }}>
                {version ? "v" + version : "本地"}
              </div>
            </Box>
            <Tooltip content={pending.length ? `${pending.length} 个待审批` : "没有待审批"}>
              <IconButton size="1" aria-label="通知" onClick={() => setNotif(true)}>
                <span style={{ position: "relative", display: "inline-flex" }}>
                  <Bell size={14} />
                  {pending.length ? <span className="notif-dot">{pending.length > 9 ? "9+" : pending.length}</span> : null}
                </span>
              </IconButton>
            </Tooltip>
            <Tooltip content="设置">
              <IconButton size="1" aria-label="设置" onClick={() => onOpenSettings?.()}><Settings size={14} /></IconButton>
            </Tooltip>
          </div>
        )}
      </div>

      {/* Rename dialog */}
      <Dialog.Root open={!!renaming} onOpenChange={(o) => !o && setRenaming(null)}>
        <Dialog.Content>
          <Dialog.Title>重命名会话</Dialog.Title>
          <Dialog.Description>名称只影响列表里的显示。</Dialog.Description>
          <TextField.Root value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="例如：内网 8899 存活侦察" />
          <Flex gap="2" mt="4" justify="end">
            <Dialog.Close><Button variant="ghost">取消</Button></Dialog.Close>
            <Button variant="solid" onClick={async () => {
              try { await patchTask(renaming.id, { name: newName.trim() }); setRenaming(null); toast?.("已重命名"); }
              catch (e) { toast?.(e.message, true); }
            }}>保存</Button>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>

      {/* Approval queue */}
      <Dialog.Root open={notif} onOpenChange={setNotif}>
        <Dialog.Content>
          <Dialog.Title>待审批</Dialog.Title>
          <Dialog.Description>
            {pending.length ? `${pending.length} 个调用等待裁决；超过时限会按规则自动处理。` : "没有等待处理的审批。"}
          </Dialog.Description>
          {pending.map((p) => (
            <Box key={p.id} style={{ borderTop: "0.5px solid var(--c-border-1)", padding: "10px 0" }}>
              <Flex align="center" gap="2">
                <ShieldAlert size={13} color="var(--c-status-awaiting)" />
                <Text size="2" weight="medium">{p.tool || "工具"}</Text>
                <Badge variant="outline">#{p.id}</Badge>
                <Box style={{ marginLeft: "auto" }} />
                <Button size="1" variant="soft" color="red" onClick={() => decide(p.id, "deny")}>拒绝</Button>
                <Button size="1" variant="solid" onClick={() => decide(p.id, "allow")}>允许</Button>
              </Flex>
              <Text size="1" color="gray" as="div" className="mono" style={{ marginTop: 6, maxHeight: 80, overflow: "auto" }}>
                {p.input || p.detail || ""}
              </Text>
            </Box>
          ))}
        </Dialog.Content>
      </Dialog.Root>

      {/* About */}
      <Dialog.Root open={about} onOpenChange={setAbout}>
        <Dialog.Content>
          <Dialog.Title>artex-harness</Dialog.Title>
          <Dialog.Description>单 agent 的渗透执行骨架：循环、状态、调度、留痕、干预、评测。</Dialog.Description>
          <Flex direction="column" gap="2">
            <Flex justify="between"><Text size="2" color="gray">版本</Text><Text size="2" className="mono">{version || "dev"}</Text></Flex>
            <Flex justify="between"><Text size="2" color="gray">会话</Text><Text size="2" className="mono">{tasks.length}</Text></Flex>
            <Flex justify="between"><Text size="2" color="gray">待审批</Text><Text size="2" className="mono">{pending.length}</Text></Flex>
          </Flex>
          <Separator style={{ margin: "14px 0" }} />
          <Text size="1" color="gray" as="div" style={{ lineHeight: 1.6 }}>
            仅限已获明确书面授权的系统使用。完整条款见仓库 DISCLAIMER.md。
          </Text>
          <Flex gap="2" mt="4" justify="end">
            <Button variant="ghost" onClick={() => setAbout(false)}>关闭</Button>
            <Button variant="soft" onClick={() => { onLogout?.(); setAbout(false); }}>
              <LogOut size={12} /> 退出登录
            </Button>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>
    </aside>
  );
}
