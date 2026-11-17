import { useMemo } from "react";
import { Badge, Box, Flex, IconButton, ScrollArea, Text, Tooltip } from "@radix-ui/themes";
import { Plus, X, CircleDot, TriangleAlert } from "lucide-react";

/* 多会话标签（Sprint 2）：把"打开过的会话/任务"做成一等公民。
 * 标签只是视图选择，不拥有运行——切换不中断任何在跑的轮次（后端 SSE 各自续传）。
 * 待处理审批 / 运行中的状态点让长任务在别的标签里也不丢。 */

const statusDot = (s) => s === "running" ? "amber" : s === "failed" || s === "timeout" ? "red" : s === "done" ? "green" : "gray";
/** The status ladder is literal, never the brand accent: waiting amber, done
 *  green, failure brick, everything else the muted running neutral. */
const statusColor = (s) =>
  s === "running" ? "var(--c-status-awaiting)"
    : s === "done" || s === "achieved" ? "var(--c-status-ok)"
    : s === "failed" || s === "timeout" ? "var(--c-status-bad)"
    : "var(--c-status-running)";

export default function WorkspaceTabs({ open, active, onPick, onClose, onNew, pendingByTask = {} }) {
  const list = useMemo(() => open || [], [open]);
  return (
    <Flex align="center" gap="1" className="wtabs" px="2" py="1">
      <ScrollArea scrollbars="horizontal" style={{ flex: 1, minWidth: 0 }}>
        <Flex gap="1" style={{ width: "max-content" }}>
          {list.map((tab) => {
            const id = String(tab.id);
            const isActive = String(active?.id) === id && active?.type === tab.type;
            const n = pendingByTask[id] || 0;
            return (
              <button key={tab.type + ":" + id}
                      className={"wtab" + (isActive ? " active" : "")}
                      onClick={() => onPick(tab)}
                      title={`${tab.title || id} · ${tab.type === "conv" ? "对话" : "HARNESS 运行"}`}>
                {n > 0 ? <TriangleAlert size={11} color="var(--c-status-awaiting)" /> : <CircleDot size={10} color={statusColor(tab.status)} />}
                <span className="wtab-title">{tab.title || (tab.type === "conv" ? "对话 " : "运行 #") + id}</span>
                {tab.type === "task" && tab.status === "running" ? <span className="wtab-live" /> : null}
                <span className="wtab-x" role="button" aria-label="关闭标签"
                      onClick={(e) => { e.stopPropagation(); onClose(tab); }}>
                  <X size={10} />
                </span>
              </button>
            );
          })}
        </Flex>
      </ScrollArea>
      <Tooltip content="新建对话（⌘N）">
        <IconButton size="1" variant="ghost" aria-label="新建标签" onClick={onNew}><Plus size={13} /></IconButton>
      </Tooltip>
      <Badge size="1" variant="soft" color="gray">{list.length} 标签</Badge>
    </Flex>
  );
}
