import { useMemo, useRef, useState } from "react";
import { Box, DropdownMenu, Flex, Heading, IconButton, ScrollArea, Text, Tooltip } from "@radix-ui/themes";
import { useVirtualizer } from "@tanstack/react-virtual";
import { formatDistanceToNowStrict, isThisWeek, isToday, isYesterday } from "date-fns";
import { zhCN } from "date-fns/locale";
import { Command as CommandIcon, Copy, MoreHorizontal, Search } from "lucide-react";

/* 会话列表：紧凑单行 + 时间分组 + 悬停操作 + 虚拟滚动。
 * 组内行高固定 30px，113 条也只渲染可见的那十几行（@tanstack/react-virtual）。
 * 分组用 date-fns 判断（今天/昨天/本周/更早），文案由 date-fns 的 zhCN 提供。 */

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

export default function Sidebar({ tasks, taskId, onPick, onPalette }) {
  const [hover, setHover] = useState(null);
  const scrollRef = useRef(null);

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
    <aside className="rail">
      <Flex align="center" gap="2" px="3" pt="3" pb="2">
        <Heading size="3" weight="medium">artex · harness</Heading>
        <Box style={{ marginLeft: "auto" }}>
          <Tooltip content="命令面板 ⌘K">
            <IconButton size="1" variant="ghost" color="gray" aria-label="命令面板" onClick={onPalette}>
              <CommandIcon size={14} />
            </IconButton>
          </Tooltip>
        </Box>
      </Flex>

      <Flex align="center" gap="2" px="3" pb="2">
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
              <div key={item.key} style={style}
                   className={"row" + (active ? " active" : "")}
                   onMouseEnter={() => setHover(t.id)}
                   onMouseLeave={() => setHover((h) => (h === t.id ? null : h))}
                   onClick={() => onPick(String(t.id))}
                   role="button" tabIndex={0}
                   onKeyDown={(e) => { if (e.key === "Enter") onPick(String(t.id)); }}>
                <span className="dot" style={{ background: statusColor(t.status) }} />
                <Text size="2" className="row-title">
                  {t.goal || t.description || "task " + t.id}
                </Text>
                <span className="row-tail">
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
                </span>
              </div>
            );
          })}
        </div>
      </div>
    </aside>
  );
}
