import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Avatar, Badge, Box, Button, Card, ContextMenu, Dialog, DropdownMenu, Flex, Heading, IconButton, Separator, Text, TextField as RTextField, Tooltip,
} from "@radix-ui/themes";
import { useVirtualizer } from "@tanstack/react-virtual";
import { formatDistanceToNowStrict, isThisWeek, isToday, isYesterday } from "date-fns";
import { zhCN } from "date-fns/locale";
import SettingsDialog from "./Settings.jsx";
import { Archive, Bell, Command as CommandIcon, Copy, Info, MoreHorizontal, PanelLeftClose, PanelLeftOpen, Pencil, Pin, Plus, Search, Settings, ShieldAlert } from "lucide-react";

/* 会话列表：紧凑单行 + 时间分组 + 悬停操作 + 虚拟滚动。
 * 组内行高固定 30px，113 条也只渲染可见的那十几行（@tanstack/react-virtual）。
 * 分组用 date-fns 判断（今天/昨天/本周/更早），文案由 date-fns 的 zhCN 提供。 */

function authHeaders() {
  let t = ""; try { t = localStorage.getItem("artex.harness.token") || ""; } catch {}
  return t ? { Authorization: "Bearer " + t } : {};
}

const ROW_H = 30;
const HEAD_H = 26;

function groupOf(t) {
  const ts = t.created_at ? new Date(t.created_at) : null;
  if (!ts || Number.isNaN(+ts)) return "更早";
  if (isToday(ts)) return "今天";
  if (isYesterday(ts)) return "昨天";
  if (isThisWeek(ts, { weekStartsOn: 1 })) return "本周";
  return "更早";
}

const statusColor = (s) =>
  s === "running" ? "var(--amber-9)" : s === "done" || s === "achieved" ? "var(--green-9)" : s === "failed" || s === "timeout" ? "var(--red-9)" : "var(--gray-8)";

export default function Sidebar({ tasks, taskId, convs = [], convId, onPickConv, onPick, onNew, onPalette, onFilter, profiles = [], version, toast, appearance, onAppearance, onOpenSettings }) {
  const [hover, setHover] = useState(null);
  const [about, setAbout] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => { try { return localStorage.getItem("artex.rail") === "collapsed"; } catch { return false; } });
  const [pending, setPending] = useState([]);
  const [notif, setNotif] = useState(false);
  const [renaming, setRenaming] = useState(null);
  const [newName, setNewName] = useState("");
  useEffect(() => { try { localStorage.setItem("artex.rail", collapsed ? "collapsed" : "open"); } catch {} }, [collapsed]);
  const scrollRef = useRef(null);
  const loadPending = useCallback(async () => {
    try {
      const r = await fetch("/api/intercept/pending", { credentials: "include", headers: authHeaders() });
      if (r.ok) { const d = await r.json(); setPending(Array.isArray(d) ? d : (d.items || d.pending || [])); }
    } catch { /* 通知不是关键路径 */ }
  }, []);
  useEffect(() => { loadPending(); const id = setInterval(loadPending, 15000); return () => clearInterval(id); }, [loadPending]);

  async function decide(id, decision) {
    try {
      const r = await fetch(`/api/intercept/pending/${id}/decide`, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ decision }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 140));
      toast?.(decision === "allow" ? "已允许" : "已拒绝");
      loadPending();
    } catch (e) { toast?.(e.message, true); }
  }

  async function patchTask(id, body) {
    const r = await fetch(`/api/tasks/${encodeURIComponent(id)}`, {
      method: "PATCH", credentials: "include",
      headers: { ...authHeaders(), "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.text()).slice(0, 140));
    onReload?.();
  }
  const running = tasks.filter((t) => t.status === "running").length;
  const notifCount = tasks.filter((t) => t.status === "running" || t.status === "failed").length;

  // 扁平化成 [分组头, 行, 行, 分组头, ...]，虚拟化需要一维列表
  const flat = useMemo(() => {
    const order = ["今天", "昨天", "本周", "更早"];
    const buckets = new Map(order.map((k) => [k, []]));
    for (const t of tasks) buckets.get(groupOf(t)).push(t);
    const out = [];
    for (const k of order) {
      const list = buckets.get(k);
      if (!list.length) continue;
      out.push({ kind: "head", key: "h-" + k, label: k, count: list.length });
      for (const t of list) out.push({ kind: "row", key: "t-" + t.id, task: t });
    }
    return out;
  }, [tasks]);

  const virt = useVirtualizer({
    count: flat.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: (i) => (flat[i].kind === "head" ? HEAD_H : ROW_H),
    overscan: 12,
  });

  return (
    <aside className={"rail" + (collapsed ? " collapsed" : "")}>
      {collapsed ? (
        <Flex align="center" justify="center" py="3">
          <Tooltip content="展开侧栏" side="right">
            <IconButton size="1" variant="ghost" color="gray" aria-label="展开侧栏" onClick={() => setCollapsed(false)}>
              <PanelLeftOpen size={15} />
            </IconButton>
          </Tooltip>
        </Flex>
      ) : (
        <Flex align="center" gap="2" px="3" pt="3" pb="2">
          <Heading size="3" weight="medium">artex · harness</Heading>
          <Box style={{ marginLeft: "auto" }}>
            <Tooltip content="收起侧栏">
              <IconButton size="1" variant="ghost" color="gray" aria-label="收起侧栏" onClick={() => setCollapsed(true)}>
                <PanelLeftClose size={14} />
              </IconButton>
            </Tooltip>
            <Tooltip content="命令面板 ⌘K">
              <IconButton size="1" variant="ghost" color="gray" aria-label="命令面板" onClick={onPalette}>
                <CommandIcon size={14} />
              </IconButton>
            </Tooltip>
          </Box>
        </Flex>
      )}

      {/* 新建对话放在列表正上方——所有聊天产品都是这个位置，不该只藏在标题栏。 */}
      {collapsed ? (
        <Flex px="2" pb="2" justify="center">
          <Tooltip content="新建对话">
            <IconButton size="2" variant="soft" aria-label="新建对话" onClick={onNew}><Plus size={15} /></IconButton>
          </Tooltip>
        </Flex>
      ) : (
        <Box px="3" pb="2">
          <Button size="2" variant="soft" style={{ width: "100%", justifyContent: "flex-start" }} onClick={onNew}>
            <Plus size={14} /> 新建对话
          </Button>
        </Box>
      )}

      {/* 对话：无目标、发第一条消息才开跑。和下面的任务（goal 驱动）是两种东西，
          所以在侧栏里分区显示，而不是混进同一个列表。 */}
      {!collapsed && convs.length ? (
        <Box pb="2">
          <Text size="1" color="gray" as="div" px="3" pb="1">对话</Text>
          {convs.slice(0, 6).map((c) => {
            const active = String(c.id) === String(convId);
            return (
              <div key={"c" + c.id} className={"row" + (active ? " active" : "")} role="button" tabIndex={0}
                   title={c.title || "新对话"}
                   onClick={() => onPickConv(String(c.id))}
                   onKeyDown={(e) => { if (e.key === "Enter") onPickConv(String(c.id)); }}>
                <span className="dot" style={{ background: "var(--accent-9)" }} />
                <Text size="2" className="row-title">{c.title || "新对话"}</Text>
              </div>
            );
          })}
        </Box>
      ) : null}

      <Flex align="center" gap="2" px="3" pb="2" style={collapsed ? { display: "none" } : undefined}>
        <Text size="1" color="gray">{tasks.length} 个会话</Text>
        <Box style={{ marginLeft: "auto" }}>
          <Tooltip content="⌘K 搜索">
            <IconButton size="1" variant="ghost" color="gray" aria-label="搜索" onClick={onPalette}>
              <Search size={13} />
            </IconButton>
          </Tooltip>
        </Box>
      </Flex>

      <div ref={scrollRef} style={{ flex: 1, minHeight: 0, overflow: "auto", position: "relative" }}>
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
                  <Text size="1" color="gray" weight="medium">{item.label}</Text>
                  <Text size="1" color="gray">{item.count}</Text>
                </div>
              );
            }
            const t = item.task;
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
                <span className="dot" style={{ background: statusColor(t.status) }} />
                {collapsed ? null : (
                <Text size="2" className="row-title">
                  {t.name || t.goal || t.description || "task " + t.id}
                </Text>
                )}
                {collapsed ? null : (<span className="row-tail">
                  {isHover || active ? (
                    <DropdownMenu.Root>
                      <DropdownMenu.Trigger>
                        <IconButton size="1" variant="ghost" color="gray" aria-label="会话操作"
                                    onClick={(e) => e.stopPropagation()}>
                          <MoreHorizontal size={13} />
                        </IconButton>
                      </DropdownMenu.Trigger>
                      <DropdownMenu.Content size="1" onClick={(e) => e.stopPropagation()}>
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
                        <DropdownMenu.Item color="red"
                          onSelect={async () => {
                            try {
                              const r = await fetch(`/api/tasks/${encodeURIComponent(t.id)}/archive`, { method: "POST", credentials: "include", headers: authHeaders() });
                              if (!r.ok) throw new Error((await r.text()).slice(0, 140));
                              toast?.("已归档 #" + t.id); onReload?.();
                            } catch (e) { toast?.(e.message, true); }
                          }}>
                          <Archive size={12} /> 归档
                        </DropdownMenu.Item>
                        <DropdownMenu.Separator />
                        <DropdownMenu.Item color="gray" disabled>
                          {t.status} · {t.created_at ? formatDistanceToNowStrict(new Date(t.created_at), { addSuffix: true, locale: zhCN }) : "—"}
                        </DropdownMenu.Item>
                      </DropdownMenu.Content>
                    </DropdownMenu.Root>
                  ) : (
                    <Text size="1" color="gray">
                      {t.created_at ? formatDistanceToNowStrict(new Date(t.created_at), { locale: zhCN }) : ""}
                    </Text>
                  )}
                </span>)}
              </div>
                </ContextMenu.Trigger>
                <ContextMenu.Content size="1">
                  <ContextMenu.Item onSelect={() => onPick(String(t.id))}>打开</ContextMenu.Item>
                  <ContextMenu.Item onSelect={() => navigator.clipboard?.writeText(String(t.id))}>
                    <Copy size={12} /> 复制 ID #{t.id}
                  </ContextMenu.Item>
                  <ContextMenu.Separator />
                  <ContextMenu.Item onSelect={() => patchTask(t.id, { pinned: !t.pinned }).catch((e) => toast?.(e.message, true))}>
                    <Pin size={12} /> {t.pinned ? "取消置顶" : "置顶"}
                  </ContextMenu.Item>
                  <ContextMenu.Item onSelect={() => { setRenaming(t); setNewName(t.name || ""); }}>
                    <Pencil size={12} /> 重命名
                  </ContextMenu.Item>
                  <ContextMenu.Item color="red" onSelect={async () => {
                    try {
                      const r = await fetch(`/api/tasks/${encodeURIComponent(t.id)}/archive`, { method: "POST", credentials: "include", headers: authHeaders() });
                      if (!r.ok) throw new Error((await r.text()).slice(0, 140));
                      toast?.("已归档 #" + t.id); onReload?.();
                    } catch (e) { toast?.(e.message, true); }
                  }}>
                    <Archive size={12} /> 归档
                  </ContextMenu.Item>
                </ContextMenu.Content>
              </ContextMenu.Root>
            );
          })}
        </div>
      </div>

      <Separator size="4" />
      {collapsed ? (
        <Flex direction="column" align="center" gap="2" py="3" className="rail-foot"
              style={{ flexShrink: 0, borderTop: "1px solid var(--gray-a5)" }}>
          <Tooltip content={pending.length ? `${pending.length} 个待审批` : "没有待审批"} side="right">
            <IconButton size="2" variant="ghost" color="gray" aria-label="通知" onClick={() => setNotif(true)}>
              <Box style={{ position: "relative", display: "inline-flex" }}>
                <Bell size={16} />
                {pending.length ? <span className="notif-dot">{pending.length > 9 ? "9+" : pending.length}</span> : null}
              </Box>
            </IconButton>
          </Tooltip>

          {/* 收起态只有两个入口：通知与头像。其余（展开 / 设置 / 关于）进头像菜单，
              这样矮窗口里也不会溢出。 */}
          <DropdownMenu.Root>
            <DropdownMenu.Trigger>
              <button className="avatar-btn" aria-label="账户与菜单">
                <Avatar size="2" fallback="A" radius="full" color="tomato" />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Content size="1" side="right" align="start">
              <DropdownMenu.Label>artex-harness {version ? "v" + version : ""}</DropdownMenu.Label>
              <DropdownMenu.Item onSelect={() => setCollapsed(false)}>
                <PanelLeftOpen size={12} /> 展开侧栏
              </DropdownMenu.Item>
              <DropdownMenu.Item onSelect={() => setAbout(true)}>
                <Info size={12} /> 关于
              </DropdownMenu.Item>
              <DropdownMenu.Separator />
              <DropdownMenu.Item onSelect={() => setSettingsOpen(true)}>
                <Settings size={12} /> 设置
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Root>
        </Flex>
      ) : (
        <Flex align="center" gap="2" px="3" py="2" className="rail-foot">
          <Avatar size="1" fallback="A" radius="full" color="tomato" />
          <Box style={{ minWidth: 0 }}>
            <Text size="1" weight="medium" style={{ display: "block", lineHeight: 1.2 }}>ARTEX</Text>
            <Text size="1" color="gray" truncate style={{ display: "block", fontSize: 10 }}>
              {version ? "v" + version : "版本未知"}
            </Text>
          </Box>
          <Box style={{ marginLeft: "auto" }}>
            <Flex gap="1" align="center">
              <Tooltip content={pending.length ? `${pending.length} 个待审批` : "没有待审批"}>
                <IconButton size="1" variant="ghost" color="gray" aria-label="通知" onClick={() => setNotif(true)}>
                  <Box style={{ position: "relative", display: "inline-flex" }}>
                    <Bell size={14} />
                    {pending.length ? <span className="notif-dot">{pending.length > 9 ? "9+" : pending.length}</span> : null}
                  </Box>
                </IconButton>
              </Tooltip>
              <DropdownMenu.Root>
                <IconButton size="1" variant="ghost" color="gray" aria-label="设置" onClick={() => onOpenSettings?.()}>
                  <Settings size={14} />
                </IconButton>
                <DropdownMenu.Trigger style={{ display: "none" }}><span /></DropdownMenu.Trigger>
                <DropdownMenu.Content size="1" align="end">
                  <DropdownMenu.Label>模型</DropdownMenu.Label>
                  {profiles.slice(0, 6).map((pr) => (
                    <DropdownMenu.Item key={pr.id} onSelect={() => toast?.("模型切换在输入框下方")}>
                      {pr.name || pr.model}{pr.is_default ? " · 当前" : ""}
                    </DropdownMenu.Item>
                  ))}

                </DropdownMenu.Content>
              </DropdownMenu.Root>
              <Tooltip content="关于">
                <IconButton size="1" variant="ghost" color="gray" aria-label="关于" onClick={() => setAbout(true)}><Info size={14} /></IconButton>
              </Tooltip>
            </Flex>
          </Box>
        </Flex>
      )}

      <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen}
                      appearance={appearance} onAppearance={onAppearance}
                      version={version} toast={toast} />

      <Dialog.Root open={!!renaming} onOpenChange={(o) => !o && setRenaming(null)}>
        <Dialog.Content maxWidth="420px">
          <Dialog.Title size="4">重命名会话</Dialog.Title>
          <RTextField.Root value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="例如：内网 8899 存活侦察" mt="3" />
          <Flex gap="3" mt="4" justify="end">
            <Dialog.Close><button className="ghost-btn">取消</button></Dialog.Close>
            <IconButton variant="solid" onClick={async () => {
              try { await patchTask(renaming.id, { name: newName.trim() }); setRenaming(null); toast?.("已重命名"); }
              catch (e) { toast?.(e.message, true); }
            }}>保存</IconButton>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>

      <Dialog.Root open={notif} onOpenChange={setNotif}>
        <Dialog.Content maxWidth="520px">
          <Dialog.Title size="4">需要你处理</Dialog.Title>
          <Dialog.Description size="1" color="gray">
            平台审批门挂起的动作；不处理会一直等着。
          </Dialog.Description>
          <Flex direction="column" gap="2" mt="3">
            {!pending.length ? <Text size="1" color="gray">当前没有待审批动作。</Text> : null}
            {pending.map((p) => (
              <Card key={p.id} size="1">
                <Flex direction="column" gap="1">
                  <Flex align="center" gap="2">
                    <ShieldAlert size={13} color="var(--amber-9)" />
                    <Text size="1" weight="medium">{p.tool_name || p.tool || "工具调用"}</Text>
                    <Text size="1" color="gray">#{p.id}</Text>
                  </Flex>
                  {p.reason ? <Text size="1" color="gray">{p.reason}</Text> : null}
                  <Flex gap="2" justify="end">
                    <IconButton size="1" variant="soft" color="red" onClick={() => decide(p.id, "deny")}>拒绝</IconButton>
                    <IconButton size="1" variant="solid" onClick={() => decide(p.id, "allow")}>允许</IconButton>
                  </Flex>
                </Flex>
              </Card>
            ))}
          </Flex>
        </Dialog.Content>
      </Dialog.Root>

      <Dialog.Root open={about} onOpenChange={setAbout}>
        <Dialog.Content maxWidth="420px">
          <Dialog.Title size="4">artex-harness</Dialog.Title>
          <Dialog.Description size="1" color="gray">任务式渗透测试会话控制台</Dialog.Description>
          <Flex direction="column" gap="2" mt="3">
            <Row k="版本" v={version || "未知"} />
            <Row k="会话" v={`${tasks.length} 个`} />
            <Row k="运行中" v={`${running} 个`} />
            <Row k="事件协议" v="AG-UI（SSE）" />
            <Row k="界面依赖" v="Radix Themes · lucide · motion · cmdk · recharts · sonner" />
          </Flex>
        </Dialog.Content>
      </Dialog.Root>
    </aside>
  );
}

const Row = ({ k, v }) => (
  <Flex justify="between" gap="3">
    <Text size="1" color="gray">{k}</Text>
    <Text size="1">{v}</Text>
  </Flex>
);
