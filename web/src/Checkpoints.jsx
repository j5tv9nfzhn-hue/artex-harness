import { useCallback, useEffect, useState } from "react";
import { Badge, Box, Button, Card, Checkbox, Dialog, Flex, IconButton, ScrollArea, Separator, Text } from "@radix-ui/themes";
import { RotateCcw, ShieldAlert } from "lucide-react";
import { toast } from "sonner";

/* 检查点与回滚（Sprint 2）。
 * 每次轮次开始时后端对工作区做哈希快照；这里列出还原点、预览影响范围、
 * 再显式执行。刻意不做"一键撤销"：回滚会删掉快照之后新增的文件，
 * 必须让操作者先看到 deleted / untracked 列表。 */

const authHeaders = () => {
  let t = ""; try { t = localStorage.getItem("artex_token") || localStorage.getItem("artex.harness.token") || ""; } catch {}
  return t ? { Authorization: "Bearer " + t } : {};
};
async function api(path, init) {
  const r = await fetch(path, { headers: authHeaders(), credentials: "include", ...(init || {}) });
  if (!r.ok) throw new Error((await r.text()).slice(0, 200) || r.statusText);
  return r.json();
}

export default function Checkpoints({ taskId, onRestored }) {
  const [cps, setCps] = useState([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [plan, setPlan] = useState(null);
  const [rev, setRev] = useState(null);
  const [confirmCode, setConfirmCode] = useState(false);

  const load = useCallback(async () => {
    if (!taskId) { setCps([]); return; }
    try {
      const d = await api(`/api/harness/checkpoints/${encodeURIComponent(taskId)}`);
      setCps(d.checkpoints || []); setErr("");
    } catch (e) { setErr(e.message); }
  }, [taskId]);
  useEffect(() => { load(); }, [load]);

  const preview = async (r) => {
    setRev(r); setPlan(null); setConfirmCode(false); setBusy(true);
    try { const d = await api(`/api/harness/checkpoints/${encodeURIComponent(taskId)}/${r}`); setPlan(d.plan); }
    catch (e) { toast.error("预览失败：" + e.message); setRev(null); }
    finally { setBusy(false); }
  };

  const restore = async () => {
    if (!plan || !rev) return;
    setBusy(true);
    try {
      await api(`/api/harness/checkpoints/${encodeURIComponent(taskId)}/${rev}/restore`, { method: "POST" });
      toast.success(`已回滚到第 ${rev} 轮之前`);
      setPlan(null); setRev(null); setConfirmCode(false);
      load(); onRestored?.();
    } catch (e) { toast.error("回滚失败：" + e.message); }
    finally { setBusy(false); }
  };

  if (!taskId) return <Box p="3"><Text size="2" color="gray">选择任务后显示还原点。</Text></Box>;

  return (
    <Flex direction="column" height="100%" minHeight="0">
      <Flex align="center" gap="2" px="3" py="2">
        <Text size="2" weight="medium">检查点</Text>
        <Badge size="1" variant="soft">{cps.length} 个还原点</Badge>
        <Box style={{ marginLeft: "auto" }} />
        <IconButton size="1" variant="ghost" aria-label="刷新" onClick={load}>↻</IconButton>
      </Flex>
      <Separator size="4" />
      {err ? <Box px="3" py="2"><Text size="2" color="red">{err} <Button size="1" variant="soft" onClick={load}>重试</Button></Text></Box> : null}
      {!err && !cps.length ? <Box px="3" py="3"><Text size="2" color="gray">尚无还原点。每轮开始时自动生成；历史任务（创建于本功能之前）没有快照。</Text></Box> : null}
      <ScrollArea style={{ flex: 1 }}>
        <Flex direction="column" gap="2" p="3">
          {cps.slice().reverse().map((c) => (
            <Card key={c.revision} size="1">
              <Flex align="center" gap="2">
                <Box>
                  <Text size="2" weight="medium">第 {c.revision} 轮</Text>
                  <Text size="1" color="gray" as="div">{new Date(c.created_at).toLocaleString()} · {c.files.length} 个文件</Text>
                </Box>
                <Box style={{ marginLeft: "auto" }} />
                <Button size="1" variant="soft" disabled={busy} onClick={() => preview(c.revision)}>
                  <RotateCcw size={12} /> 预览回滚
                </Button>
              </Flex>
            </Card>
          ))}
        </Flex>
      </ScrollArea>

      <Dialog.Root open={!!plan} onOpenChange={(o) => { if (!o) { setPlan(null); setRev(null); } }}>
        <Dialog.Content maxWidth="560px">
          <Dialog.Title>回滚到第 {rev} 轮之前</Dialog.Title>
          <Dialog.Description size="1" color="gray">
            这会修改任务工作区。以下范围来自快照预览，请逐条确认。
          </Dialog.Description>
          {plan ? (
            <Flex direction="column" gap="3" mt="3">
              <PlanList title="将恢复（快照中的常规文件）" items={plan.restorable} color="green" />
              <PlanList title="将重新创建（快照有、现在没有）" items={plan.added} color="blue" />
              <PlanList title="将删除（现在有、快照没有）" items={plan.deleted} color="red" />
              <PlanList title="不会回滚（保持现状）" items={[...(plan.skip || []), ...(plan.untracked || [])]} color="amber" />
              <Card size="1" style={{ borderColor: "var(--amber-7)" }}>
                <Flex gap="2">
                  <ShieldAlert size={14} color="var(--amber-9)" />
                  <Text size="2" style={{ flex: 1 }}>{plan.note}</Text>
                </Flex>
              </Card>
              <Text size="1" color="gray" as="label">
                <Flex gap="2" align="center">
                  <Checkbox checked={confirmCode} onCheckedChange={setConfirmCode} />
                  我理解回滚会删除上述「将删除」的文件，且 Shell 写入不会回滚
                </Flex>
              </Text>
            </Flex>
          ) : null}
          <Flex gap="3" mt="4" justify="end">
            <Dialog.Close><button className="ghost-btn">取消</button></Dialog.Close>
            <Button color="red" disabled={!confirmCode || busy} onClick={restore}>{busy ? "回滚中…" : "确认回滚"}</Button>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>
    </Flex>
  );
}

function PlanList({ title, items, color }) {
  if (!items?.length) return null;
  return (
    <Box>
      <Text size="1" color={color} weight="medium" as="div" mb="1">{title}（{items.length}）</Text>
      <Box className="mono" style={{ maxHeight: 110, overflow: "auto", fontSize: 11, lineHeight: 1.6 }}>
        {items.map((x, i) => <div key={i}>· {x}</div>)}
      </Box>
    </Box>
  );
}
