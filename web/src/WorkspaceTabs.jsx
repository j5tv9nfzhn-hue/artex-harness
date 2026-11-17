import { useMemo } from "react";
import { Plus, X } from "lucide-react";

/* 多会话标签：把"打开过的会话/任务"做成一等公民。
 *
 * 标签只是视图选择，不拥有运行——切换不中断任何在跑的轮次（后端 SSE 各自续传）。
 * 一条标签 = 一个会话，形态照参考产品的标签条：32px 高、6px 圆角、当前标签用
 * 面板色（比画布亮一档），其余透明；状态只用一个小圆点表示，不写文字。 */

const statusColor = (s) =>
  s === "running" ? "var(--c-status-awaiting)"
    : s === "done" || s === "achieved" ? "var(--c-status-ok)"
    : s === "failed" || s === "timeout" ? "var(--c-status-bad)"
    : "var(--c-ink-4)";

export default function WorkspaceTabs({ open, active, onPick, onClose, onNew, pendingByTask = {} }) {
  const list = useMemo(() => open || [], [open]);
  if (!list.length) return null;
  return (
    <div className="wtabs">
      <div className="wtabs__scroll">
        {list.map((tab) => {
          const id = String(tab.id);
          const isActive = String(active?.id) === id && active?.type === tab.type;
          const n = pendingByTask[id] || 0;
          return (
            <button key={tab.type + ":" + id}
                    className={"wtab" + (isActive ? " active" : "")}
                    onClick={() => onPick(tab)}
                    title={`${tab.title || id} · ${tab.type === "conv" ? "对话" : "HARNESS 运行"}`}>
              <span className="wtab-dot" style={{ background: n > 0 ? "var(--c-status-awaiting)" : statusColor(tab.status) }} />
              <span className="wtab-title">{tab.title || (tab.type === "conv" ? "对话 " : "运行 #") + id}</span>
              <span className="wtab-x" role="button" aria-label="关闭标签"
                    onClick={(e) => { e.stopPropagation(); onClose(tab); }}>
                <X size={10} />
              </span>
            </button>
          );
        })}
        <button className="wtab wtab--new" onClick={onNew} aria-label="新建标签" title="新建对话">
          <Plus size={12} />
        </button>
      </div>
    </div>
  );
}
