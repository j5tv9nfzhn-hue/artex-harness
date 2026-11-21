import { authedFetch, errorText } from "./lib/auth.js";
import { useEffect, useState } from "react";import { Badge, Box, Button, Card, Checkbox, Code, Flex, Heading, IconButton, ScrollArea, Separator, Dialog, Select, Switch, Table, Text, TextArea, TextField, Tooltip } from "./ui.jsx";
import { ArrowLeft, ArrowUpCircle, Check, Cpu, FileText, KeyRound, Palette, Pencil, Plug, Plus, RefreshCw, Shield, SlidersHorizontal, Sparkles, Terminal, Trash2, Wand2 } from "lucide-react";

/* 设置：与 DSH 相同的形态——打开后【左侧栏变成设置导航】，主区显示该分区的表单。
 * 不是弹窗、不是上下堆叠卡片、不是弹窗内的 tab。
 *   左侧 = 分区枚举（供应商 / 插件·MCP / 技能 / 权限 / 会话日志 / 运行环境 /
 *   运行开关 / 更新 / 账户安全 / 外观）
 *   右侧 = 选中分区的表单
 *
 * ── 设置页的统一标准（3.27；Julie 指出此前各分区是随意拼上去的）──
 * 每个分区都按同一骨架排布，禁止各写各的：
 *   ① SectionIntro：一句灰色小字，说明这个分区管什么；
 *   ② 列表：Card 分组（models 的供应商卡片是样板）或 ghost Table，
 *      行内操作统一用 IconButton size="1" variant="ghost"
 *     （编辑 Pencil / 删除 Trash2 红色 / 刷新 RefreshCw / 设为默认 Check），
 *      启用态统一用 Switch；
 *   ③ 底部主操作：Button size="2" variant="soft" + Plus；
 *   ④ 空列表统一用 EmptyState。
 * 对话框统一：Title size 4 + Description + Field 行 + 底部右对齐 [取消][保存]。
 * 只读展示（env/logs）只有 ①，没有 ③④。
 * Select 的触发面必须传 children 显示当前值（只给 placeholder 时它永远写着
 * “选择”，看着像没选——见 Composer 同款修复）。
 */

// 统一走 lib/auth.js：token 键与请求重试都由那一处决定（此前这里只读单个键，
// 与 api 层的键不一致时设置页的请求会全部 401）。
const jfetch = (p, o = {}) =>
  authedFetch(p, { credentials: "include", ...o, headers: { ...(o.body ? { "Content-Type": "application/json" } : {}) } })
    .then(async (r) => { if (!r.ok) throw new Error(await errorText(r)); return r.headers.get("content-type")?.includes("json") ? r.json() : null; });

const SECTIONS = [
  { id: "models", label: "供应商与模型", icon: Cpu },
  { id: "mcp", label: "插件 · MCP", icon: Plug },
  { id: "skills", label: "技能", icon: Sparkles },
  { id: "perm", label: "权限与审批", icon: Shield },
  { id: "logs", label: "会话日志", icon: FileText },
  { id: "env", label: "运行环境", icon: Terminal },
  { id: "run", label: "运行开关", icon: SlidersHorizontal },
  { id: "update", label: "更新", icon: ArrowUpCircle },
  { id: "account", label: "账户安全", icon: KeyRound },
  { id: "look", label: "外观", icon: Palette },
];

export default function SettingsView({ open, onOpenChange, initialSection, appearance, onAppearance, version, health, toast }) {
  const [section, setSection] = useState("models");
  // 每次打开都定位到调用方指定的分区（默认 models；输入框的权限芯片会指定 perm）。
  // 只在 open 翻转时重置，打开后用户自己切分区不受影响。
  useEffect(() => { if (open) setSection(initialSection || "models"); }, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  const [profiles, setProfiles] = useState([]);
  const [mcps, setMcps] = useState([]);
  const [skills, setSkills] = useState([]);
  const [rules, setRules] = useState([]);
  const [logs, setLogs] = useState([]);
  const [err, setErr] = useState("");
  const [addProvider, setAddProvider] = useState(false);   // 新增供应商
  const [addModelFor, setAddModelFor] = useState(null);     // 给某供应商加模型
  const [editProvider, setEditProvider] = useState(null);   // 编辑供应商端点
  const [editModel, setEditModel] = useState(null);         // 编辑模型参数

  useEffect(() => {
    jfetch("/api/llm/profiles").then((d) => setProfiles(Array.isArray(d) ? d : d.profiles || [])).catch((e) => setErr(e.message));
    jfetch("/api/mcp").then((d) => setMcps(Array.isArray(d) ? d : d.servers || [])).catch(() => {});
    jfetch("/api/skills").then((d) => setSkills(Array.isArray(d) ? d : d.skills || [])).catch(() => {});
    jfetch("/api/intercept/rules").then((d) => setRules(Array.isArray(d) ? d : d.rules || [])).catch(() => {});
  }, []);
  useEffect(() => {
    if (section === "logs" && !logs.length) jfetch("/api/llm/records").then((d) => setLogs(Array.isArray(d) ? d : d.records || [])).catch(() => {});
  }, [section]); // eslint-disable-line react-hooks/exhaustive-deps

  async function reloadProfiles() {
    try { const d = await jfetch("/api/llm/profiles"); setProfiles(Array.isArray(d) ? d : d.profiles || []); } catch (e) { setErr(e.message); }
  }

  async function activate(id) {
    try { await jfetch("/api/llm/profiles/active", { method: "POST", body: JSON.stringify({ id: Number(id) }) });
      toast.success("已切换默认模型");
      const d = await jfetch("/api/llm/profiles"); setProfiles(Array.isArray(d) ? d : d.profiles || []);
    } catch (e) { toast.error(e.message); }
  }
  async function remove(p) {
    try { await jfetch(`/api/llm/profiles/${p.id}`, { method: "DELETE" }); toast.success("已删除");
      const d = await jfetch("/api/llm/profiles"); setProfiles(Array.isArray(d) ? d : d.profiles || []);
    } catch (e) { toast.error(e.message); }
  }

  /* ── MCP / 拦截规则 / 技能：后端早就有完整的增删改查（见 server.go 的
   *    /api/mcp、/api/intercept/rules、/api/skills），此前设置页只接了 GET，
   *    三个分区都是只读展示——这就是“一个名字一个按钮就完事”的根源。
   *    这里把写口全部接上，与 models 分区同款：卡片/表格 + 行内操作 +
   *    底部主操作 + 对话框。 */
  const [mcpBusy, setMcpBusy] = useState({});
  const [mcpDialog, setMcpDialog] = useState(null);   // null | "new" | MCPServer
  const [mcpTick, setMcpTick] = useState(0);          // 刷新后让各卡片重读工具列表
  const [ruleDialog, setRuleDialog] = useState(null); // null | "new" | InterceptRule
  const [skillDialog, setSkillDialog] = useState(false);
  async function reloadMCP() {
    try { const d = await jfetch("/api/mcp"); setMcps(Array.isArray(d) ? d : d.servers || []); } catch (e) { setErr(e.message); }
  }
  async function reloadRules() {
    try { const d = await jfetch("/api/intercept/rules"); setRules(Array.isArray(d) ? d : d.rules || []); } catch (e) { setErr(e.message); }
  }
  async function reloadSkills() {
    try { const d = await jfetch("/api/skills"); setSkills(Array.isArray(d) ? d : d.skills || []); } catch (e) { setErr(e.message); }
  }
  async function saveMCP(body) {
    try {
      await jfetch("/api/mcp", { method: "POST", body: JSON.stringify(body) });
      toast.success(body.id ? "MCP 服务已更新" : "MCP 服务已添加");
      setMcpDialog(null); reloadMCP();
    } catch (e) { toast.error(e.message); }
  }
  async function toggleMCP(m) {
    if (mcpBusy[m.id]) return;
    setMcpBusy((b) => ({ ...b, [m.id]: true }));
    try {
      await jfetch("/api/mcp", { method: "POST", body: JSON.stringify({ ...m, enabled: !m.enabled }) });
      setMcps((ps) => ps.map((x) => (x.id === m.id ? { ...x, enabled: !m.enabled } : x)));
    } catch (e) { toast.error(e.message); }
    finally { setMcpBusy((b) => ({ ...b, [m.id]: false })); }
  }
  async function deleteMCP(m) {
    try { await jfetch(`/api/mcp/${m.id}`, { method: "DELETE" }); toast.success("已删除"); reloadMCP(); }
    catch (e) { toast.error(e.message); }
  }
  async function refreshMCP(m) {
    if (mcpBusy[m.id]) return;
    setMcpBusy((b) => ({ ...b, [m.id]: true }));
    try {
      await jfetch(`/api/mcp/${m.id}/refresh`, { method: "POST", body: JSON.stringify({}) });
      toast.success("工具列表已刷新"); setMcpTick((t) => t + 1);
    } catch (e) { toast.error(e.message); }
    finally { setMcpBusy((b) => ({ ...b, [m.id]: false })); }
  }
  async function saveRule(body) {
    try {
      if (body.id) await jfetch(`/api/intercept/rules/${body.id}`, { method: "PUT", body: JSON.stringify(body) });
      else await jfetch("/api/intercept/rules", { method: "POST", body: JSON.stringify(body) });
      toast.success(body.id ? "规则已更新" : "规则已添加");
      setRuleDialog(null); reloadRules();
    } catch (e) { toast.error(e.message); }
  }
  async function toggleRule(r) {
    try {
      await jfetch(`/api/intercept/rules/${r.id}/toggle`, { method: "POST", body: JSON.stringify({ enabled: !r.enabled }) });
      setRules((ps) => ps.map((x) => (x.id === r.id ? { ...x, enabled: !r.enabled } : x)));
    } catch (e) { toast.error(e.message); }
  }
  async function deleteRule(r) {
    try { await jfetch(`/api/intercept/rules/${r.id}`, { method: "DELETE" }); toast.success("已删除"); reloadRules(); }
    catch (e) { toast.error(e.message); }
  }
  async function saveSkill(body) {
    try {
      await jfetch("/api/skills", { method: "POST", body: JSON.stringify(body) });
      toast.success("技能已新建"); setSkillDialog(false); reloadSkills();
    } catch (e) { toast.error(e.message); }
  }
  async function deleteSkill(s) {
    try { await jfetch(`/api/skills/${encodeURIComponent(s.name)}`, { method: "DELETE" }); toast.success("已删除"); reloadSkills(); }
    catch (e) { toast.error(e.message); }
  }

  // toast 在 App 里是 {success, error, info} 对象（见 toast.jsx），不可当函数调用；
  // 老代码的 toast?.(msg, isErr) 调用形会在运行时抛错，已全改为 toast.success/error，
  // 新代码的 say() 只是同一写法的兼容壳。
  const say = (msg, isErr) => {
    if (!toast) return;
    if (isErr) { if (toast.error) toast.error(msg); else if (typeof toast === "function") toast(msg, true); }
    else { if (toast.success) toast.success(msg); else if (typeof toast === "function") toast(msg); }
  };

  /* ── 运行开关：GET/PUT /api/settings（扁平键值，见 server/server.go 的
   *    settingsPayload / putSettings；PUT 只发改动的键，后端按 *bool/*int
   *    做部分更新）。未读到之前不渲染开关，只显示加载态，不闪现错误值。 */
  const [runSettings, setRunSettings] = useState(null);
  const [runErr, setRunErr] = useState("");
  const [runBusy, setRunBusy] = useState({});
  const [concLimitDraft, setConcLimitDraft] = useState("");
  const loadRunSettings = async () => {
    setRunErr("");
    try {
      const d = await jfetch("/api/settings");
      setRunSettings(d || {});
      setConcLimitDraft(String(d?.task_concurrency_limit ?? 5));
    } catch (e) { setRunErr(e.message); }
  };
  useEffect(() => {
    if (section === "run" && runSettings === null && !runErr) loadRunSettings();
  }, [section]); // eslint-disable-line react-hooks/exhaustive-deps
  async function toggleRun(key, label) {
    if (!runSettings || runBusy[key]) return;
    const next = !runSettings[key];
    const prev = runSettings;
    setRunBusy((b) => ({ ...b, [key]: true }));
    setRunSettings((s) => ({ ...s, [key]: next }));
    try {
      await jfetch("/api/settings", { method: "PUT", body: JSON.stringify({ [key]: next }) });
      say(`${label}已${next ? "开启" : "关闭"}`);
    } catch (e) {
      setRunSettings(prev);
      say(`${label}切换失败：${e.message}`, true);
    } finally {
      setRunBusy((b) => ({ ...b, [key]: false }));
    }
  }
  async function saveConcLimit() {
    const n = Number(concLimitDraft);
    if (!Number.isInteger(n) || n < 1) { say("并发上限需为 ≥ 1 的整数", true); return; }
    setRunBusy((b) => ({ ...b, task_concurrency_limit: true }));
    try {
      await jfetch("/api/settings", { method: "PUT", body: JSON.stringify({ task_concurrency_limit: n }) });
      setRunSettings((s) => ({ ...s, task_concurrency_limit: n }));
      say(`任务并发上限已设为 ${n}`);
    } catch (e) { say(`保存失败：${e.message}`, true); }
    finally { setRunBusy((b) => ({ ...b, task_concurrency_limit: false })); }
  }

  /* ── 自更新：GET /api/update/check（?force=1 强制回源）、
   *    POST /api/update/apply（202 后台下载）、POST /api/update/rollback、
   *    GET /api/update/stream（SSE 推送 {phase, percent, message, version, error}）。
   *    应用/回滚成功后服务端退出、由守护脚本拉起（退出码 75），UI 只提示重启。 */
  const [upd, setUpd] = useState(null);
  const [updErr, setUpdErr] = useState("");
  const [updBusy, setUpdBusy] = useState(false);
  const [updProg, setUpdProg] = useState(null);
  const [updLines, setUpdLines] = useState([]);
  async function checkUpdate(force) {
    setUpdBusy(true); setUpdErr("");
    try {
      const d = await jfetch(`/api/update/check${force ? "?force=1" : ""}`);
      setUpd(d);
    } catch (e) { setUpdErr(e.message); }
    finally { setUpdBusy(false); }
  }
  useEffect(() => {
    if (section !== "update") return;
    checkUpdate(false);
    // 流用 cookie 鉴权（EventSource 不能设请求头，见 LoginGate）。
    const es = new EventSource("/api/update/stream");
    es.onmessage = (e) => {
      try {
        const p = JSON.parse(e.data);
        setUpdProg(p);
        if (p.message) setUpdLines((ls) => [...ls.slice(-49), `${UPD_PHASE[p.phase] || p.phase || ""} ${p.message}${p.percent >= 0 ? `（${p.percent}%）` : ""}`.trim()]);
      } catch { /* 忽略非 JSON 帧 */ }
    };
    return () => es.close();
  }, [section]); // eslint-disable-line react-hooks/exhaustive-deps
  async function applyUpdate() {
    if (!upd?.has_update) return;
    setUpdBusy(true);
    try {
      const d = await jfetch("/api/update/apply", { method: "POST", body: JSON.stringify({}) });
      setUpdLines((ls) => [...ls, `已提交更新到 ${d?.target || upd.latest}，开始下载…`]);
      say(`已开始更新到 ${d?.target || upd.latest}，服务即将重启，请稍后刷新`);
    } catch (e) { say(`应用更新失败：${e.message}`, true); }
    finally { setUpdBusy(false); }
  }
  async function rollbackUpdate() {
    setUpdBusy(true);
    try {
      await jfetch("/api/update/rollback", { method: "POST", body: JSON.stringify({}) });
      say("已回滚到上一版本，服务即将重启，请稍后刷新");
    } catch (e) { say(`回滚失败：${e.message}`, true); }
    finally { setUpdBusy(false); }
  }

  /* ── 改密码：POST /api/auth/change-password
   *    {old_password, new_password}（见 d46c53b 的 server/auth.go；
   *    后端要求新密码至少 8 位、不超过 72 字节）。 */
  const [oldPw, setOldPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [newPw2, setNewPw2] = useState("");
  const [pwBusy, setPwBusy] = useState(false);
  async function changePassword() {
    if (newPw.length < 8) { say("新密码至少 8 位", true); return; }
    if (newPw !== newPw2) { say("两次输入的新密码不一致", true); return; }
    setPwBusy(true);
    try {
      await jfetch("/api/auth/change-password", { method: "POST", body: JSON.stringify({ old_password: oldPw, new_password: newPw }) });
      say("密码已修改，下次登录请用新密码");
      setOldPw(""); setNewPw(""); setNewPw2("");
    } catch (e) { say(`修改密码失败：${e.message}`, true); }
    finally { setPwBusy(false); }
  }

  const providers = groupProviders(profiles);

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Content className="settings-dialog" maxWidth="none"
                      style={{ width: "min(920px, 94vw)", height: "min(640px, 86vh)", padding: 0, overflow: "hidden" }}>
      <div className="settings-grid">
      {/* 弹窗内的左栏：设置导航 */}
      <aside className="settings-rail" aria-label="设置导航">
        <Flex align="center" gap="2" px="3" pt="3" pb="2">
          <Tooltip content="返回会话">
            <IconButton size="1" variant="ghost" color="gray" aria-label="返回会话" onClick={() => onOpenChange(false)}><ArrowLeft size={15} /></IconButton>
          </Tooltip>
          <Dialog.Title><Heading size="3" weight="medium" style={{ margin: 0 }}>设置</Heading></Dialog.Title>
        </Flex>
        <ScrollArea style={{ flex: 1 }}>
          <Flex direction="column" gap="1" px="2" py="2">
            {SECTIONS.map((s) => {
              const Icon = s.icon;
              const active = section === s.id;
              return (
                <button key={s.id} className={"set-nav" + (active ? " active" : "")} onClick={() => setSection(s.id)}>
                  <Icon size={14} />
                  <span>{s.label}</span>
                </button>
              );
            })}
          </Flex>
        </ScrollArea>
        <Separator size="4" />
        <Box px="3" py="2"><Text size="1" color="gray">artex-harness {version ? "v" + version : ""}</Text></Box>
      </aside>

      {/* 弹窗内的右栏：分区表单 */}
      <div className="settings-main">
        <Flex align="center" gap="2" px="5" py="3" style={{ borderBottom: "1px solid var(--c-divider)" }}>
          <Text size="3" weight="medium">{SECTIONS.find((s) => s.id === section)?.label}</Text>
        </Flex>
        <ScrollArea style={{ flex: 1 }}>
          <Box px="5" py="4" style={{ maxWidth: 900 }}>
            {err ? <Card size="1" mb="3"><Text size="1" color="red">{err}</Text></Card> : null}

            {section === "models" && (
              <Flex direction="column" gap="3">
                <SectionIntro>供应商 = 端点（协议 + Base URL + 密钥 + 代理），模型挂在其下；标“默认”的模型是新任务的默认选择。</SectionIntro>
                {providers.map((pv) => (
                  <Card key={pv.key} size="1">
                    <Flex direction="column" gap="2">
                      <Flex align="center" gap="2" wrap="wrap">
                        <Text size="2" weight="medium">{fmtLabel(pv.format)}</Text>
                        <Code size="1">{pv.base_url || "默认端点"}</Code>
                        {pv.api_key_hint ? <Badge size="1" color="green" variant="soft">密钥 {pv.api_key_hint}</Badge> : <Badge size="1" color="red" variant="soft">缺少密钥</Badge>}
                        {pv.proxy ? <Badge size="1" color="gray" variant="soft">代理 {pv.proxy}</Badge> : null}
                        <Box style={{ marginLeft: "auto" }}>
                          <Flex gap="1">
                            <Button size="1" variant="soft" color="gray" onClick={() => setEditProvider(pv)}><Pencil size={12} /> 端点</Button>
                            <Button size="1" variant="soft" onClick={() => setAddModelFor(pv)}><Plus size={12} /> 模型</Button>
                          </Flex>
                        </Box>
                      </Flex>
                      <Separator size="4" />
                      <Table.Root size="1" variant="ghost">
                        <Table.Header><Table.Row>
                          <Table.ColumnHeaderCell>模型</Table.ColumnHeaderCell>
                          <Table.ColumnHeaderCell>上下文</Table.ColumnHeaderCell>
                          <Table.ColumnHeaderCell>思考</Table.ColumnHeaderCell>
                          <Table.ColumnHeaderCell>速率</Table.ColumnHeaderCell>
                          <Table.ColumnHeaderCell />
                        </Table.Row></Table.Header>
                        <Table.Body>
                          {pv.models.map((m) => (
                            <Table.Row key={m.id} align="center">
                              <Table.Cell><Flex align="center" gap="2"><Text size="1">{m.model}</Text>{m.is_default ? <Badge size="1" color="green" variant="soft">默认</Badge> : null}</Flex></Table.Cell>
                              <Table.Cell><Text size="1" color="gray">{m.context_window_k ? m.context_window_k + "K" : "默认"}</Text></Table.Cell>
                              <Table.Cell><Text size="1" color="gray">{[m.thinking_type, m.reasoning_effort].filter(Boolean).join(" / ") || "默认"}</Text></Table.Cell>
                              <Table.Cell><Text size="1" color="gray">{m.rate_per_second ? m.rate_per_second + "/s" : m.rate_per_minute ? m.rate_per_minute + "/min" : "不限"}</Text></Table.Cell>
                              <Table.Cell><Flex gap="1" justify="end">
                                <IconButton size="1" variant="ghost" aria-label="编辑" onClick={() => setEditModel(m)}><Pencil size={12} /></IconButton>
                                {m.is_default ? null : <IconButton size="1" variant="ghost" aria-label="设为默认" onClick={() => activate(m.id)}><Check size={12} /></IconButton>}
                                <IconButton size="1" variant="ghost" color="red" aria-label="删除" onClick={() => remove(m)}><Trash2 size={12} /></IconButton>
                              </Flex></Table.Cell>
                            </Table.Row>
                          ))}
                        </Table.Body>
                      </Table.Root>
                    </Flex>
                  </Card>
                ))}
                <Button size="2" variant="soft" onClick={() => setAddProvider(true)}><Plus size={13} /> 添加供应商</Button>
                {!providers.length ? <EmptyState>还没有供应商。供应商 = 端点（协议 + Base URL + 密钥 + 代理），模型挂在其下。</EmptyState> : null}
              </Flex>
            )}

            {section === "mcp" && (
              <Flex direction="column" gap="3">
                <SectionIntro>MCP 服务给 agent 提供外部工具。添加后自动发现工具列表；工具需按 agent 可见性授权后才能被调用。</SectionIntro>
                {mcps.map((m) => (
                  <Card key={m.id || m.name} size="1">
                    <Flex direction="column" gap="2">
                      <Flex align="center" gap="2" wrap="wrap">
                        <Text size="2" weight="medium">{m.name}</Text>
                        <Code size="1">{m.transport || "—"}</Code>
                        {m.url ? <Code size="1">{m.url}</Code> : m.command ? <Code size="1">{m.command}</Code> : null}
                        {m.command && parseArgs(m.args).length ? <Text size="1" color="gray">{parseArgs(m.args).length} 个参数</Text> : null}
                        <Box style={{ marginLeft: "auto" }}>
                          <Flex gap="1" align="center">
                            <Switch checked={!!m.enabled} disabled={!!mcpBusy[m.id]} onCheckedChange={() => toggleMCP(m)} aria-label="启用" />
                            <IconButton size="1" variant="ghost" aria-label="重新发现工具" disabled={!!mcpBusy[m.id]} onClick={() => refreshMCP(m)}><RefreshCw size={12} /></IconButton>
                            <IconButton size="1" variant="ghost" aria-label="编辑" onClick={() => setMcpDialog(m)}><Pencil size={12} /></IconButton>
                            <IconButton size="1" variant="ghost" color="red" aria-label="删除" onClick={() => deleteMCP(m)}><Trash2 size={12} /></IconButton>
                          </Flex>
                        </Box>
                      </Flex>
                      <Separator size="4" />
                      <MCPTools serverId={m.id} tick={mcpTick} />
                    </Flex>
                  </Card>
                ))}
                {!mcps.length ? <EmptyState>还没有 MCP 服务。添加后 agent 可以调用它提供的工具。</EmptyState> : null}
                <Button size="2" variant="soft" onClick={() => setMcpDialog("new")}><Plus size={13} /> 添加 MCP 服务</Button>
              </Flex>
            )}

            {section === "skills" && (
              <Flex direction="column" gap="3">
                <SectionIntro>技能是 agent 可调用的能力包，存在服务端的 skills/ 目录；调用次数来自实际使用记录。</SectionIntro>
                {skills.map((s) => (
                  <Card key={s.name || s} size="1">
                    <Flex direction="column" gap="2">
                      <Flex align="center" gap="2" wrap="wrap">
                        <Text size="2" weight="medium">{s.name || s}</Text>
                        {(s.mcps || []).map((n) => <Badge key={n} size="1" variant="soft">{n}</Badge>)}
                        <Box style={{ marginLeft: "auto" }}>
                          <Flex gap="1" align="center">
                            <Text size="1" color="gray">调用 {s.calls ?? 0} 次 · {s.tasks ?? 0} 个任务</Text>
                            <IconButton size="1" variant="ghost" color="red" aria-label="删除" onClick={() => deleteSkill(s)}><Trash2 size={12} /></IconButton>
                          </Flex>
                        </Box>
                      </Flex>
                      {s.description ? <Text size="1" color="gray" style={{ lineHeight: 1.6 }}>{s.description}</Text> : null}
                      <Text size="1" color="gray">
                        {(s.files || []).length} 个文件
                        {s.last_used ? ` · 最近使用 ${String(s.last_used).replace("T", " ").slice(0, 19)}` : " · 从未调用"}
                      </Text>
                    </Flex>
                  </Card>
                ))}
                {!skills.length ? <EmptyState>还没有技能。新建一个，或把技能目录放进服务端的 skills/ 文件夹。</EmptyState> : null}
                <Button size="2" variant="soft" onClick={() => setSkillDialog(true)}><Plus size={13} /> 新建技能</Button>
              </Flex>
            )}

            {section === "perm" && (
              <Flex direction="column" gap="3">
                <SectionIntro>拦截规则决定工具调用是放行、拦截还是转人工审批；平台内置的破坏性命令拦截始终生效。</SectionIntro>
                {rules.length ? (
                  <Table.Root size="1" variant="ghost">
                    <Table.Header><Table.Row>
                      <Table.ColumnHeaderCell>规则</Table.ColumnHeaderCell>
                      <Table.ColumnHeaderCell>动作</Table.ColumnHeaderCell>
                      <Table.ColumnHeaderCell>匹配</Table.ColumnHeaderCell>
                      <Table.ColumnHeaderCell>内容</Table.ColumnHeaderCell>
                      <Table.ColumnHeaderCell>启用</Table.ColumnHeaderCell>
                      <Table.ColumnHeaderCell />
                    </Table.Row></Table.Header>
                    <Table.Body>
                      {rules.map((r) => (
                        <Table.Row key={r.id} align="center">
                          <Table.Cell><Text size="1">{r.name}</Text></Table.Cell>
                          <Table.Cell><Badge size="1" variant="soft" color={r.action === "deny" ? "red" : r.action === "ask" ? "amber" : "green"}>{ACTION_LABEL[r.action] || r.action || "—"}</Badge></Table.Cell>
                          <Table.Cell><Text size="1" color="gray">{TARGET_LABEL[r.match_target] || r.match_target} · {TYPE_LABEL[r.match_type] || r.match_type}</Text></Table.Cell>
                          <Table.Cell><Code size="1">{r.pattern}</Code></Table.Cell>
                          <Table.Cell><Switch checked={!!r.enabled} onCheckedChange={() => toggleRule(r)} aria-label="启用" /></Table.Cell>
                          <Table.Cell><Flex gap="1" justify="end">
                            <IconButton size="1" variant="ghost" aria-label="编辑" onClick={() => setRuleDialog(r)}><Pencil size={12} /></IconButton>
                            <IconButton size="1" variant="ghost" color="red" aria-label="删除" onClick={() => deleteRule(r)}><Trash2 size={12} /></IconButton>
                          </Flex></Table.Cell>
                        </Table.Row>
                      ))}
                    </Table.Body>
                  </Table.Root>
                ) : <EmptyState>还没有自定义拦截规则。</EmptyState>}
                <Button size="2" variant="soft" onClick={() => setRuleDialog("new")}><Plus size={13} /> 添加规则</Button>
              </Flex>
            )}

            {section === "logs" && (
              <Flex direction="column" gap="3">
                <SectionIntro>最近 60 条 LLM 调用记录：输入输出 token 与状态。</SectionIntro>
                <Table.Root size="1" variant="surface">
                <Table.Header><Table.Row>
                  <Table.ColumnHeaderCell>时间</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>模型</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>输入</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>输出</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>状态</Table.ColumnHeaderCell>
                </Table.Row></Table.Header>
                <Table.Body>
                  {logs.slice(0, 60).map((r, i) => (
                    <Table.Row key={r.id || i} align="center">
                      <Table.Cell><Text size="1" color="gray">{(r.created_at || "").replace("T", " ").slice(0, 19)}</Text></Table.Cell>
                      <Table.Cell><Code size="1">{r.model || "—"}</Code></Table.Cell>
                      <Table.Cell><Text size="1">{r.input_tokens ?? "—"}</Text></Table.Cell>
                      <Table.Cell><Text size="1">{r.output_tokens ?? "—"}</Text></Table.Cell>
                      <Table.Cell><Badge size="1" variant="soft" color={r.status === "ok" ? "green" : "red"}>{r.status || "—"}</Badge></Table.Cell>
                    </Table.Row>
                  ))}
                  {!logs.length ? <Table.Row><Table.Cell colSpan={5}><Text size="1" color="gray">暂无记录</Text></Table.Cell></Table.Row> : null}
                </Table.Body>
              </Table.Root>
              </Flex>
            )}

            {section === "env" && (
              <Flex direction="column" gap="3">
                <SectionIntro>运行环境是只读事实：服务、版本与运行参数来自服务端与启动配置。</SectionIntro>
                <Row k="服务" v={health?.service || "artex-harness"} />
                <Row k="版本" v={health?.version || version || "—"} />
                <Row k="监听" v={window.location.host} />
                <Row k="事件协议" v="AG-UI（SSE）" />
                <Row k="单轮墙钟预算" v="20 分钟（HARNESS_RUN_BUDGET）" />
                <Row k="最大轮次" v="40（agents.max_turns）" />
                <Row k="子代理超时" v="10 分钟（HARNESS_SUBAGENT_TIMEOUT）" />
              </Flex>
            )}

            {section === "run" && (
              <Flex direction="column" gap="3">
                <SectionIntro>开关即时保存；流量捕获与 LLM 轮询切换后，后台会重建 agent 以使新状态生效。</SectionIntro>
                {runErr ? (
                  <Card size="1"><Text size="1" color="red">{runErr} <Button size="1" variant="soft" onClick={loadRunSettings}>重试</Button></Text></Card>
                ) : !runSettings ? (
                  <Text size="1" color="gray">读取中…</Text>
                ) : (
                  <>
                    {RUN_TOGGLES.map((t) => (
                      <Flex key={t.key} align="center" gap="3" style={{ maxWidth: 640 }}>
                        <Box style={{ flex: 1, minWidth: 0 }}>
                          <Text size="2">{t.label}</Text>
                          <Text size="1" color="gray" style={{ display: "block", lineHeight: 1.6 }}>{t.hint}</Text>
                          {t.key === "web_search_enabled" && runSettings.web_search_backend ? (
                            <Text size="1" color="gray" style={{ display: "block" }}>后端：{runSettings.web_search_backend}</Text>
                          ) : null}
                        </Box>
                        <Switch checked={!!runSettings[t.key]} disabled={!!runBusy[t.key]}
                                onCheckedChange={() => toggleRun(t.key, t.label)} />
                      </Flex>
                    ))}
                    <Separator size="4" />
                    <Flex align="center" gap="3" style={{ maxWidth: 640 }}>
                      <Box style={{ flex: 1, minWidth: 0 }}>
                        <Text size="2">任务并发上限</Text>
                        <Text size="1" color="gray" style={{ display: "block", lineHeight: 1.6 }}>
                          同时运行的任务数上限；仅在「任务并发上限」开启时生效，超限的新任务排队等待。
                        </Text>
                      </Box>
                      <TextField.Root size="2" style={{ width: 72 }} value={concLimitDraft}
                        disabled={!runSettings.task_concurrency_enabled}
                        onChange={(e) => setConcLimitDraft(e.target.value)} />
                      <Button size="1" variant="soft"
                        disabled={!runSettings.task_concurrency_enabled || !!runBusy.task_concurrency_limit}
                        onClick={saveConcLimit}>保存</Button>
                    </Flex>
                  </>
                )}
              </Flex>
            )}

            {section === "update" && (
              <Flex direction="column" gap="3">
                <SectionIntro>检查并应用服务端更新；应用或回滚后服务将重启，请稍后刷新页面。</SectionIntro>
                {updErr ? (
                  <Card size="1"><Text size="1" color="red">{updErr} <Button size="1" variant="soft" onClick={() => checkUpdate(true)}>重试</Button></Text></Card>
                ) : null}
                {!upd && !updErr ? <Text size="1" color="gray">读取中…</Text> : null}
                {upd ? (
                  <>
                    <Flex direction="column" gap="2">
                      <Row k="当前版本" v={upd.current || "—"} />
                      <Row k="最新版本" v={upd.latest || "未查到"} />
                      <Row k="运行方式" v={upd.mode || "—"} />
                      <Row k="平台" v={[upd.os, upd.arch].filter(Boolean).join(" / ") || "—"} />
                      {upd.has_backup ? <Row k="备份" v="有上一版本备份，可回滚" /> : null}
                      {upd.published_at ? <Row k="发布时间" v={String(upd.published_at).replace("T", " ").slice(0, 19)} /> : null}
                    </Flex>
                    {upd.error ? <Text size="1" color="red">检查失败：{upd.error}</Text> : null}
                    {upd.boot_notice ? <Text size="1" color="amber" style={{ lineHeight: 1.6 }}>{upd.boot_notice}</Text> : null}
                    {upd.rolled_back ? <Text size="1" color="amber">上次启动检测到回滚。</Text> : null}
                    {upd.comparable === false && upd.reason ? <Text size="1" color="gray" style={{ lineHeight: 1.6 }}>{upd.reason}</Text> : null}
                    {upd.has_update ? (
                      <Flex align="center" gap="2">
                        <Badge size="1" variant="soft" color="green">有新版本 {upd.latest}</Badge>
                        {upd.asset_available === false ? <Text size="1" color="amber">当前平台暂无对应安装包</Text> : null}
                      </Flex>
                    ) : upd.latest ? (
                      <Badge size="1" variant="soft" color="gray">已是最新版本</Badge>
                    ) : null}
                    {upd.notes ? (
                      <Text size="1" color="gray" style={{ lineHeight: 1.6, whiteSpace: "pre-wrap" }}>
                        {String(upd.notes).slice(0, 500)}
                      </Text>
                    ) : null}
                  </>
                ) : null}
                <Flex gap="2" wrap="wrap">
                  <Button size="2" variant="soft" onClick={() => checkUpdate(true)} disabled={updBusy}>
                    <RefreshCw size={13} className={updBusy ? "spin" : ""} /> 检查更新
                  </Button>
                  <Button size="2" disabled={!upd?.has_update || updBusy || updProg?.phase === "downloading"}
                          onClick={applyUpdate}>应用更新</Button>
                  <Button size="2" variant="soft" color="gray" disabled={!upd?.has_backup || updBusy}
                          onClick={rollbackUpdate}>回滚到上一版本</Button>
                </Flex>
                {updProg && updProg.phase !== "idle" ? (
                  <Flex align="center" gap="2">
                    <Text size="2" weight="medium">{UPD_PHASE[updProg.phase] || updProg.phase}</Text>
                    {updProg.percent >= 0 ? <Text size="1" color="gray">{updProg.percent}%</Text> : null}
                    {updProg.version ? <Code size="1">{updProg.version}</Code> : null}
                    {updProg.error ? <Text size="1" color="red">{updProg.error}</Text> : null}
                  </Flex>
                ) : (
                  <Text size="1" color="gray">当前无进行中的更新。</Text>
                )}
                {updLines.length ? <pre className="replay-md">{updLines.join("\n")}</pre> : null}
              </Flex>
            )}

            {section === "account" && (
              <Flex direction="column" gap="3" style={{ maxWidth: 420 }}>
                <SectionIntro>修改管理员密码；新密码至少 8 位、不超过 72 字节，修改成功后当前会话不受影响，下次登录请用新密码。</SectionIntro>
                <Field label="当前密码">
                  <TextField.Root type="password" value={oldPw} onChange={(e) => setOldPw(e.target.value)} autoComplete="current-password" />
                </Field>
                <Field label="新密码">
                  <TextField.Root type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} autoComplete="new-password" placeholder="至少 8 位" />
                </Field>
                <Field label="确认新密码">
                  <TextField.Root type="password" value={newPw2} onChange={(e) => setNewPw2(e.target.value)} autoComplete="new-password" />
                </Field>
                <Flex gap="2">
                  <Button size="2" disabled={pwBusy || !oldPw || !newPw} onClick={changePassword}>
                    {pwBusy ? "修改中…" : "修改密码"}
                  </Button>
                </Flex>
              </Flex>
            )}

            {section === "look" && (
              <Flex direction="column" gap="3">
                <SectionIntro>外观跟随系统，或固定浅色 / 深色。</SectionIntro>
                <Flex align="center" gap="3">
                  <Text size="2">主题</Text>
                  {["inherit", "light", "dark"].map((v) => (
                    <Button key={v} size="1" variant={appearance === v ? "solid" : "soft"} onClick={() => onAppearance(v)}>
                      {v === "inherit" ? "跟随系统" : v === "light" ? "浅色" : "深色"}
                    </Button>
                  ))}
                </Flex>
              </Flex>
            )}
          </Box>
        </ScrollArea>
      </div>
      </div>

      <ProviderDialog open={addProvider} onClose={() => setAddProvider(false)}
        onSave={async (b) => { try {
          for (const m of b.models) {
            await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify({ ...m, name: m.model, format: b.format, base_url: b.base_url, proxy: b.proxy, api_key: b.api_key }) });
          }
          toast.success(b.models.length > 1 ? `已添加供应商与 ${b.models.length} 个模型` : "已添加供应商");
          setAddProvider(false); reloadProfiles();
        } catch (e) { toast.error(e.message); } }} />
      <ProviderDialog open={!!addModelFor} seed={addModelFor} onClose={() => setAddModelFor(null)}
        onSave={async (b) => { try {
          for (const m of b.models) {
            await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify({ ...m, name: m.model, format: addModelFor.format, base_url: addModelFor.base_url, proxy: addModelFor.proxy, api_key: "" }) });
          }
          toast.success(`已添加 ${b.models.length} 个模型`); setAddModelFor(null); reloadProfiles();
        } catch (e) { toast.error(e.message); } }} />
      <EditProviderDialog pv={editProvider} onClose={() => setEditProvider(null)}
        onSave={async (patch) => { try {
          for (const m of editProvider.models) await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify({ ...m, api_key: patch.api_key || "", base_url: patch.base_url, proxy: patch.proxy }) });
          toast.success("供应商已更新"); setEditProvider(null); reloadProfiles();
        } catch (e) { toast.error(e.message); } }} />
      <EditModelDialog model={editModel} onClose={() => setEditModel(null)}
        onSave={async (b) => { try { await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify(b) }); toast.success("模型已更新"); setEditModel(null); reloadProfiles(); } catch (e) { toast.error(e.message); } }} />
      <MCPDialog server={mcpDialog} onClose={() => setMcpDialog(null)} onSave={saveMCP} />
      <RuleDialog rule={ruleDialog} onClose={() => setRuleDialog(null)} onSave={saveRule} />
      <SkillDialog open={skillDialog} onClose={() => setSkillDialog(false)} onSave={saveSkill} />
      </Dialog.Content>
    </Dialog.Root>
  );
}

const Row = ({ k, v }) => (
  <Flex justify="between" gap="3" style={{ maxWidth: 520 }}>
    <Text size="1" color="gray">{k}</Text>
    <Text size="1">{v}</Text>
  </Flex>
);

/* 运行开关的键名与 settingsPayload / putSettings 一致（bool，除并发上限为 int）。
 * 顺序即界面顺序：前 5 个是任务要求的必接开关，后两个同属同一扁平设置，顺手接上。 */
const RUN_TOGGLES = [
  { key: "traffic_capture", label: "流量捕获", hint: "开启后 agent 流量经录制代理，可绑定到发现做证据；关闭后 agent 不再挂代理、不再有流量工具。" },
  { key: "agent_traffic_binding", label: "Agent 自动绑定流量", hint: "任务结束时自动把录到的流量绑定到本次任务的发现。" },
  { key: "llm_pool_enabled", label: "LLM 轮询（故障转移）", hint: "当前模型配置不可用时，自动切到下一个已配置的模型。" },
  { key: "task_concurrency_enabled", label: "任务并发上限", hint: "开启后同时运行的任务数受上限约束，超限的新任务排队等待。" },
  { key: "web_search_enabled", label: "联网搜索", hint: "允许 agent 使用联网搜索工具；后端为 web_search_backend。" },
  { key: "llm_record", label: "LLM 调用录制", hint: "记录每次 LLM 调用的输入输出 token，见「会话日志」分区。" },
  { key: "notify_enabled", label: "漏洞 IM 推送", hint: "新发现按配置的渠道推送；渠道本身在 /api/notify/* 管理，这里只开关总闸。" },
];

/* 自更新 SSE 的 phase 取值（见 selfupdate/stage.go）。 */
const UPD_PHASE = { idle: "空闲", downloading: "下载中", verifying: "校验中", extracting: "解压中", staged: "已暂存", failed: "失败" };

const FORMATS = ["openai", "anthropic", "openai-responses"];
const fmtLabel = (f) => ({ openai: "OpenAI 兼容", anthropic: "Anthropic", "openai-responses": "OpenAI Responses" }[f] || f || "—");

function groupProviders(profiles) {
  const map = new Map();
  for (const p of profiles) {
    const key = `${p.format || "openai"}|${p.base_url || ""}|${p.proxy || ""}`;
    if (!map.has(key)) map.set(key, { key, format: p.format, base_url: p.base_url, proxy: p.proxy, api_key_hint: p.api_key_hint, models: [] });
    map.get(key).models.push(p);
  }
  return [...map.values()];
}

/* 供应商表单：新增供应商（协议+端点+密钥+代理+首个模型）／给已有供应商加模型 */
function ProviderDialog({ open, seed, onClose, onSave }) {
  const isNew = !seed;
  const [format, setFormat] = useState("openai");
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const [proxy, setProxy] = useState("");
  const [manual, setManual] = useState("");
  const [busy, setBusy] = useState(false);
  const [probe, setProbe] = useState(null);   // {ok,error,endpoint,count}
  const [list, setList] = useState(null);     // LLMModelDetail[] | null
  const [picked, setPicked] = useState({});   // model -> detail
  const [q, setQ] = useState("");
  useEffect(() => {
    if (!open) return;
    setFormat(seed?.format || "openai"); setBaseURL(seed?.base_url || "");
    setProxy(seed?.proxy || ""); setAPIKey("");
    setManual(""); setProbe(null); setList(null); setPicked({}); setQ("");
  }, [open, seed]);

  // 真去问供应商要模型列表；端点自己报的容量优先，缺的由公开目录补。
  const discover = async () => {
    setBusy(true); setProbe(null);
    try {
      const r = await jfetch("/api/llm/models", {
        method: "POST",
        body: JSON.stringify({
          provider: format, base_url: baseURL, api_key: apiKey, proxy,
          profile_id: seed?.id ? Number(seed.id) : undefined,
        }),
      });
      if (!r?.ok) { setProbe({ ok: false, error: r?.error || "未获取到模型列表" }); setList(null); return; }
      const byId = new Map((r.details || []).map((d) => [d.model, d]));
      const rows = (r.models || []).map((id) => byId.get(id) || { model: id, context_source: "unknown" });
      setList(rows);
      setProbe({ ok: true, endpoint: r.endpoint, count: rows.length });
      setPicked(Object.fromEntries(rows.map((d) => [d.model, d])));
    } catch (e) { setProbe({ ok: false, error: e.message }); setList(null); }
    finally { setBusy(false); }
  };

  const toggle = (d) => setPicked((p) => { const n = { ...p }; if (n[d.model]) delete n[d.model]; else n[d.model] = d; return n; });
  const chosen = Object.values(picked);
  const shown = (list || []).filter((d) => !q.trim() || d.model.toLowerCase().includes(q.trim().toLowerCase()));
  const ctxLabel = (d) => (d.context_window_k ? `${d.context_window_k}K` : "未知");
  const ctxColor = (d) => (d.context_source === "endpoint" ? "green" : d.context_window_k ? "blue" : "gray");

  const submit = () => {
    const rows = chosen.length ? chosen : (manual.trim() ? [{ model: manual.trim(), context_source: "unknown" }] : []);
    if (!rows.length) return;
    onSave({
      name: rows[0].model, format, base_url: baseURL, proxy, api_key: apiKey,
      models: rows.map((d) => ({
        model: d.model,
        context_window_k: d.context_window_k || 0,
        thinking_type: d.thinking_type || "",
        reasoning_effort: d.reasoning_effort || "",
      })),
    });
  };

  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="760px">
        <Dialog.Title size="4">{isNew ? "添加供应商" : "添加模型"}</Dialog.Title>
        <Dialog.Description size="1" color="gray">
          {isNew ? "供应商 = 端点与凭据。填好后点「获取可用模型」，勾选要用的模型即可——上下文与思考档位会按公开目录自动带出。" : `端点沿用 ${seed?.base_url || "默认端点"}，密钥沿用已保存的那把。`}
        </Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          {isNew ? (<>
            <Field label="协议">
              <Select.Root value={format} onValueChange={setFormat}>
                <Select.Trigger style={{ width: "100%" }}>{fmtLabel(format)}</Select.Trigger>
                <Select.Content>
                  <Select.Item value="openai">OpenAI 兼容</Select.Item>
                  <Select.Item value="anthropic">Anthropic</Select.Item>
                  <Select.Item value="openai-responses">OpenAI Responses</Select.Item>
                </Select.Content>
              </Select.Root>
            </Field>
            <Field label="Base URL"><TextField.Root value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://…/v1" /></Field>
            <Field label="API Key"><TextField.Root type="password" value={apiKey} onChange={(e) => setAPIKey(e.target.value)} placeholder="sk-…" /></Field>
            <Field label="出站代理（可选）"><TextField.Root value={proxy} onChange={(e) => setProxy(e.target.value)} placeholder="http://127.0.0.1:3128" /></Field>
          </>) : null}

          <Flex align="center" gap="3">
            <Button size="2" variant="soft" onClick={discover} disabled={busy || !baseURL || (isNew && !apiKey)}>
              <RefreshCw size={13} className={busy ? "spin" : ""} /> {busy ? "正在获取…" : "获取可用模型"}
            </Button>
            <Text size="1" color="gray">
              {probe?.ok ? `端点返回 ${probe.count} 个模型` : probe?.error ? "" : "不填也能手动输入模型名"}
            </Text>
          </Flex>
          {probe?.error ? (
            <Box p="2" style={{ border: "1px solid var(--red-7)", borderRadius: 8, background: "var(--red-2)" }}>
              <Text size="1" color="red">获取失败：{probe.error}</Text>
            </Box>
          ) : null}

          {list ? (
            <Flex direction="column" gap="2">
              <TextField.Root size="2" value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索模型…" />
              <ScrollArea style={{ maxHeight: 260 }}>
                <Flex direction="column" gap="1" pr="2">
                  {shown.map((d) => (
                    <Flex key={d.model} align="center" gap="2" p="1" style={{ borderRadius: 6, background: picked[d.model] ? "var(--accent-a3)" : "transparent" }}>
                      <Checkbox checked={!!picked[d.model]} onCheckedChange={() => toggle(d)} />
                      <Code size="1" style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis" }}>{d.model}</Code>
                      {d.thinking_type ? <Badge size="1" variant="soft" color="purple">思考</Badge> : null}
                      <Tooltip content={d.context_source === "endpoint" ? "端点上报" : d.context_source === "catalog" ? "公开目录" : "两边都不知道，需手填"}>
                        <Badge size="1" variant="soft" color={ctxColor(d)}>{ctxLabel(d)}</Badge>
                      </Tooltip>
                    </Flex>
                  ))}
                  {!shown.length ? <Text size="1" color="gray">没有匹配的模型</Text> : null}
                </Flex>
              </ScrollArea>
              {chosen.length ? (
                <Flex align="center" gap="3">
                  <Text size="1" color="gray">已选 {chosen.length} 个</Text>
                  <Button size="1" variant="ghost" onClick={() => setPicked({})}>全不选</Button>
                  <Button size="1" variant="ghost" onClick={() => setPicked(Object.fromEntries(shown.map((d) => [d.model, d])))}>全选当前 {shown.length}</Button>
                </Flex>
              ) : null}
            </Flex>
          ) : (
            <Field label="模型名（手动）"><TextField.Root value={manual} onChange={(e) => setManual(e.target.value)} placeholder="例如 MiniMax-M3.1-Flash" /></Field>
          )}
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close><Button variant="soft" color="gray">取消</Button></Dialog.Close>
          <Button onClick={submit} disabled={!chosen.length && !manual.trim()}>
            {chosen.length > 1 ? `添加 ${chosen.length} 个模型` : "保存"}
          </Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

function EditProviderDialog({ pv, onClose, onSave }) {
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const [proxy, setProxy] = useState("");
  useEffect(() => { if (pv) { setBaseURL(pv.base_url || ""); setProxy(pv.proxy || ""); setAPIKey(""); } }, [pv]);
  return (
    <Dialog.Root open={!!pv} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="480px">
        <Dialog.Title size="4">编辑供应商端点</Dialog.Title>
        <Dialog.Description size="1" color="gray">应用到此供应商下的 {pv?.models?.length || 0} 个模型；密钥留空保留原值。</Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          <Field label="Base URL"><TextField.Root value={baseURL} onChange={(e) => setBaseURL(e.target.value)} /></Field>
          <Field label="API Key"><TextField.Root type="password" value={apiKey} onChange={(e) => setAPIKey(e.target.value)} placeholder="留空保留" /></Field>
          <Field label="出站代理"><TextField.Root value={proxy} onChange={(e) => setProxy(e.target.value)} /></Field>
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close><Button variant="soft" color="gray">取消</Button></Dialog.Close>
          <Button onClick={() => onSave({ base_url: baseURL, api_key: apiKey, proxy })}>保存</Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

function EditModelDialog({ model, onClose, onSave }) {
  const [ctx, setCtx] = useState("");
  const [thinking, setThinking] = useState("");
  const [effort, setEffort] = useState("");
  const [rps, setRps] = useState("");
  const [rpm, setRpm] = useState("");
  const [src, setSrc] = useState("");
  // 手输模型名时也能按公开目录回填：老配置补上下文，是最常见的诉求。
  const fillFromCatalog = async () => {
    try {
      const r = await jfetch(`/api/llm/model-catalog?q=${encodeURIComponent(model?.model || "")}`);
      const name = (model?.model || "").toLowerCase();
      const hit = (r?.entries || []).find((e) => name === e.match || name.startsWith(e.match));
      if (!hit) { setSrc("目录里没有这个模型，参数需手填"); return; }
      if (hit.context_window_k) setCtx(String(hit.context_window_k));
      if (hit.thinking_type) setThinking(hit.thinking_type);
      if (hit.default_effort) setEffort(hit.default_effort);
      setSrc(`已按 ${hit.match} 填充（公开目录）`);
    } catch (e) { setSrc("填充失败：" + e.message); }
  };
  useEffect(() => {
    if (!model) return;
    setCtx(model.context_window_k ? String(model.context_window_k) : "");
    setThinking(model.thinking_type || ""); setEffort(model.reasoning_effort || "");
    setRps(model.rate_per_second ? String(model.rate_per_second) : "");
    setRpm(model.rate_per_minute ? String(model.rate_per_minute) : "");
    setSrc("");
  }, [model]);
  return (
    <Dialog.Root open={!!model} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="520px">
        <Flex align="center" gap="3">
          <Dialog.Title size="4" style={{ flex: 1 }}>编辑模型 {model?.model}</Dialog.Title>
          <Button size="1" variant="soft" onClick={fillFromCatalog}><Wand2 size={12} /> 按目录填充</Button>
        </Flex>
        {src ? <Text size="1" color="gray" as="p" mt="1">{src}</Text> : null}
        <Flex direction="column" gap="3" mt="3">
          <Flex gap="3">
            <Field label="上下文 K"><TextField.Root value={ctx} onChange={(e) => setCtx(e.target.value)} /></Field>
            <Field label="每秒请求"><TextField.Root value={rps} onChange={(e) => setRps(e.target.value)} placeholder="不限" /></Field>
            <Field label="每分钟请求"><TextField.Root value={rpm} onChange={(e) => setRpm(e.target.value)} placeholder="不限" /></Field>
          </Flex>
          <Flex gap="3">
            <Field label="思考开关">
              <Select.Root value={thinking || "none"} onValueChange={(v) => setThinking(v === "none" ? "" : v)}>
                <Select.Trigger style={{ width: "100%" }}>{thinking === "enabled" ? "开启" : thinking === "disabled" ? "关闭" : "不发送"}</Select.Trigger>
                <Select.Content>
                  <Select.Item value="none">不发送</Select.Item>
                  <Select.Item value="enabled">开启</Select.Item>
                  <Select.Item value="disabled">关闭</Select.Item>
                </Select.Content>
              </Select.Root>
            </Field>
            <Field label="思考强度">
              <Select.Root value={effort || "none"} onValueChange={(v) => setEffort(v === "none" ? "" : v)}>
                <Select.Trigger style={{ width: "100%" }}>{effort || "不发送"}</Select.Trigger>
                <Select.Content>
                  <Select.Item value="none">不发送</Select.Item>
                  {["low", "medium", "high", "xhigh", "max"].map((v) => <Select.Item key={v} value={v}>{v}</Select.Item>)}
                </Select.Content>
              </Select.Root>
            </Field>
          </Flex>
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close><Button variant="soft" color="gray">取消</Button></Dialog.Close>
          <Button onClick={() => onSave({ ...model, context_window_k: Number(ctx) || 0, thinking_type: thinking, reasoning_effort: effort, rate_per_second: Number(rps) || 0, rate_per_minute: Number(rpm) || 0 })}>保存</Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

const Field = ({ label, children }) => (
  <Box style={{ flex: 1, minWidth: 120 }}>
    <Text size="1" color="gray" as="div" mb="1">{label}</Text>
    {children}
  </Box>
);

/* ── 统一标准共用件（3.27）── */
const SectionIntro = ({ children }) => (
  <Text size="1" color="gray" style={{ lineHeight: 1.6 }}>{children}</Text>
);
const EmptyState = ({ children }) => (
  <Text size="1" color="gray" style={{ lineHeight: 1.6 }}>{children}</Text>
);

const ACTION_LABEL = { allow: "放行", ask: "转人工审批", deny: "拦截" };
const TARGET_LABEL = { tool_name: "工具名", tool_input: "工具参数" };
const TYPE_LABEL = { string: "精确匹配", regex: "正则匹配" };

/* MCP 的 args 是 JSON 数组、env 是 JSON 对象；对话框里按“每行一个”编辑。 */
const parseArgs = (raw) => {
  if (!raw) return [];
  if (Array.isArray(raw)) return raw.filter((x) => typeof x === "string");
  if (typeof raw === "string") { try { const v = JSON.parse(raw); return Array.isArray(v) ? v : []; } catch { return []; } }
  return [];
};
const envToLines = (raw) => {
  if (!raw) return "";
  let obj = raw;
  if (typeof raw === "string") { try { obj = JSON.parse(raw); } catch { return ""; } }
  if (!obj || typeof obj !== "object" || Array.isArray(obj)) return "";
  return Object.entries(obj).map(([k, v]) => `${k}=${v}`).join("\n");
};
const linesToEnv = (text) => {
  const env = {};
  for (const line of text.split("\n")) {
    const s = line.trim();
    if (!s) continue;
    const i = s.indexOf("=");
    if (i <= 0) throw new Error(`环境变量格式不对：${s}（应为 KEY=VALUE）`);
    env[s.slice(0, i).trim()] = s.slice(i + 1).trim();
  }
  return env;
};

/* MCP 卡片里的工具列表：GET /api/mcp/{id}/tools（缓存的发现结果）。
 * 顶层组件，用 authedFetch 直接读（jfetch 只活在 SettingsView 作用域内）。 */
function MCPTools({ serverId, tick }) {
  const [tools, setTools] = useState(null); // null = 读取中
  const [err, setErr] = useState("");
  useEffect(() => {
    let dead = false;
    setTools(null); setErr("");
    authedFetch(`/api/mcp/${serverId}/tools`, { credentials: "include" })
      .then(async (r) => { if (!r.ok) throw new Error(await errorText(r)); return r.json(); })
      .then((d) => { if (!dead) setTools(d.tools || []); })
      .catch((e) => { if (!dead) setErr(e.message); });
    return () => { dead = true; };
  }, [serverId, tick]);
  if (err) return <Text size="1" color="red">工具列表读取失败：{err}</Text>;
  if (tools === null) return <Text size="1" color="gray">正在读取工具…</Text>;
  if (!tools.length) return <Text size="1" color="gray">暂无工具：可能尚未发现成功，点右上角刷新重新发现。</Text>;
  return (
    <Flex wrap="wrap" gap="2">
      {tools.map((t) => (
        <Tooltip key={t.name} content={t.description || t.name}>
          <Badge size="1" variant="soft">{t.name}</Badge>
        </Tooltip>
      ))}
    </Flex>
  );
}

/* MCP 服务表单：新增 / 编辑。transport 决定必填项（stdio 要命令，http/sse 要 URL）。 */
function MCPDialog({ server, onClose, onSave }) {
  const isNew = server === "new";
  const [name, setName] = useState("");
  const [transport, setTransport] = useState("stdio");
  const [command, setCommand] = useState("");
  const [argsText, setArgsText] = useState("");
  const [url, setUrl] = useState("");
  const [envText, setEnvText] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [insecure, setInsecure] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (!server) return;
    if (server === "new") {
      setName(""); setTransport("stdio"); setCommand(""); setArgsText("");
      setUrl(""); setEnvText(""); setEnabled(true); setInsecure(false);
    } else {
      setName(server.name || ""); setTransport(server.transport || "stdio");
      setCommand(server.command || ""); setArgsText(parseArgs(server.args).join("\n"));
      setUrl(server.url || ""); setEnvText(envToLines(server.env));
      setEnabled(!!server.enabled); setInsecure(!!server.insecure);
    }
    setErr("");
  }, [server]);
  const submit = () => {
    if (!name.trim()) { setErr("名称不能为空"); return; }
    if (transport === "stdio" && !command.trim()) { setErr("stdio 需要启动命令"); return; }
    if ((transport === "http" || transport === "sse") && !url.trim()) { setErr("http / sse 需要 URL"); return; }
    let env = {};
    try { env = linesToEnv(envText); } catch (e) { setErr(e.message); return; }
    onSave({
      id: isNew ? 0 : server.id,
      name: name.trim(), transport,
      command: transport === "stdio" ? command.trim() : "",
      args: argsText.split("\n").map((s) => s.trim()).filter(Boolean),
      env,
      url: transport === "stdio" ? "" : url.trim(),
      enabled, insecure: transport === "stdio" ? false : insecure,
    });
  };
  return (
    <Dialog.Root open={!!server} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title size="4">{isNew ? "添加 MCP 服务" : `编辑 MCP 服务 ${server?.name || ""}`}</Dialog.Title>
        <Dialog.Description size="1" color="gray">MCP 服务给 agent 提供外部工具；保存后自动发现工具列表。</Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          <Field label="名称"><TextField.Root value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 github-mcp" /></Field>
          <Field label="传输方式">
            <Select.Root value={transport} onValueChange={setTransport}>
              <Select.Trigger style={{ width: "100%" }}>{transport === "stdio" ? "stdio（本地命令）" : transport === "http" ? "http（远程 URL）" : "sse（远程 URL）"}</Select.Trigger>
              <Select.Content>
                <Select.Item value="stdio">stdio（本地命令）</Select.Item>
                <Select.Item value="http">http（远程 URL）</Select.Item>
                <Select.Item value="sse">sse（远程 URL）</Select.Item>
              </Select.Content>
            </Select.Root>
          </Field>
          {transport === "stdio" ? (<>
            <Field label="启动命令"><TextField.Root value={command} onChange={(e) => setCommand(e.target.value)} placeholder="npx" /></Field>
            <Field label="参数（每行一个）"><TextArea value={argsText} onChange={(e) => setArgsText(e.target.value)} placeholder={"-y\n@mcp/server-github"} rows={3} /></Field>
          </>) : (
            <Field label="URL"><TextField.Root value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…/mcp" /></Field>
          )}
          <Field label="环境变量（每行 KEY=VALUE，可选）"><TextArea value={envText} onChange={(e) => setEnvText(e.target.value)} placeholder="GITHUB_TOKEN=…" rows={2} /></Field>
          <Flex gap="3">
            <Field label="启用"><Switch checked={enabled} onCheckedChange={setEnabled} aria-label="启用" /></Field>
            {transport !== "stdio" ? (
              <Field label="跳过 TLS 校验（自签证书）"><Switch checked={insecure} onCheckedChange={setInsecure} aria-label="跳过 TLS 校验" /></Field>
            ) : null}
          </Flex>
          {err ? <Text size="1" color="red">{err}</Text> : null}
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close><Button variant="soft" color="gray">取消</Button></Dialog.Close>
          <Button onClick={submit}>保存</Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

/* 拦截规则表单：新增 / 编辑。枚举与后端 validateInterceptRuleReq 一致
 *（match_target: tool_name | tool_input；match_type: string | regex；
 * action: allow | deny | ask）。超时字段界面不暴露，编辑时原样保留。 */
function RuleDialog({ rule, onClose, onSave }) {
  const isNew = rule === "new";
  const [name, setName] = useState("");
  const [action, setAction] = useState("ask");
  const [matchTarget, setMatchTarget] = useState("tool_name");
  const [matchType, setMatchType] = useState("string");
  const [pattern, setPattern] = useState("");
  const [message, setMessage] = useState("");
  const [priority, setPriority] = useState("100");
  const [enabled, setEnabled] = useState(true);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (!rule) return;
    if (rule === "new") {
      setName(""); setAction("ask"); setMatchTarget("tool_name"); setMatchType("string");
      setPattern(""); setMessage(""); setPriority("100"); setEnabled(true);
    } else {
      setName(rule.name || ""); setAction(rule.action || "ask");
      setMatchTarget(rule.match_target || "tool_name"); setMatchType(rule.match_type || "string");
      setPattern(rule.pattern || ""); setMessage(rule.message || "");
      setPriority(String(rule.priority ?? 100)); setEnabled(!!rule.enabled);
    }
    setErr("");
  }, [rule]);
  const submit = () => {
    if (!name.trim()) { setErr("规则名不能为空"); return; }
    if (!pattern.trim()) { setErr("匹配内容不能为空"); return; }
    const p = Number(priority);
    if (!Number.isInteger(p)) { setErr("优先级需为整数"); return; }
    const base = isNew ? { timeout_enabled: false, timeout_seconds: 0, timeout_action: "" } : rule;
    onSave({
      ...base, id: isNew ? 0 : rule.id,
      name: name.trim(), action, match_target: matchTarget, match_type: matchType,
      pattern: pattern.trim(), message: message.trim(), priority: p, enabled,
    });
  };
  return (
    <Dialog.Root open={!!rule} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title size="4">{isNew ? "添加规则" : `编辑规则 ${rule?.name || ""}`}</Dialog.Title>
        <Dialog.Description size="1" color="gray">拦截规则决定工具调用是放行、拦截还是转人工审批。</Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          <Field label="规则名"><TextField.Root value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 禁止删除系统文件" /></Field>
          <Flex gap="3">
            <Field label="动作">
              <Select.Root value={action} onValueChange={setAction}>
                <Select.Trigger style={{ width: "100%" }}>{ACTION_LABEL[action] || action}</Select.Trigger>
                <Select.Content>
                  <Select.Item value="allow">放行</Select.Item>
                  <Select.Item value="ask">转人工审批</Select.Item>
                  <Select.Item value="deny">拦截</Select.Item>
                </Select.Content>
              </Select.Root>
            </Field>
            <Field label="优先级">
              <TextField.Root value={priority} onChange={(e) => setPriority(e.target.value)} placeholder="100" />
            </Field>
          </Flex>
          <Flex gap="3">
            <Field label="匹配对象">
              <Select.Root value={matchTarget} onValueChange={setMatchTarget}>
                <Select.Trigger style={{ width: "100%" }}>{TARGET_LABEL[matchTarget] || matchTarget}</Select.Trigger>
                <Select.Content>
                  <Select.Item value="tool_name">工具名</Select.Item>
                  <Select.Item value="tool_input">工具参数</Select.Item>
                </Select.Content>
              </Select.Root>
            </Field>
            <Field label="匹配方式">
              <Select.Root value={matchType} onValueChange={setMatchType}>
                <Select.Trigger style={{ width: "100%" }}>{TYPE_LABEL[matchType] || matchType}</Select.Trigger>
                <Select.Content>
                  <Select.Item value="string">精确匹配</Select.Item>
                  <Select.Item value="regex">正则匹配</Select.Item>
                </Select.Content>
              </Select.Root>
            </Field>
          </Flex>
          <Field label="匹配内容"><TextField.Root value={pattern} onChange={(e) => setPattern(e.target.value)} placeholder={matchType === "regex" ? "例如 ^rm\\s+-rf\\s+/" : "例如 Bash"} /></Field>
          <Field label="提示消息（命中时展示，可选）"><TextField.Root value={message} onChange={(e) => setMessage(e.target.value)} placeholder="例如 该操作已被规则拦截" /></Field>
          <Field label="启用"><Switch checked={enabled} onCheckedChange={setEnabled} aria-label="启用" /></Field>
          {err ? <Text size="1" color="red">{err}</Text> : null}
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close><Button variant="soft" color="gray">取消</Button></Dialog.Close>
          <Button onClick={submit}>保存</Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

/* 技能表单：新建。名称须符合后端 validSkillName（小写字母/数字/连字符，
 * 首尾与连续连字符除外）；描述必填。上传既有技能走服务端 skills/ 目录。 */
function SkillDialog({ open, onClose, onSave }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [license, setLicense] = useState("");
  const [compatibility, setCompatibility] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    if (!open) return;
    setName(""); setDescription(""); setLicense(""); setCompatibility(""); setErr("");
  }, [open]);
  const submit = () => {
    const n = name.trim();
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(n) || n.length > 64) { setErr("名称只允许小写字母、数字与连字符，首尾不能是连字符，最长 64 字符"); return; }
    if (!description.trim()) { setErr("描述不能为空"); return; }
    onSave({ name: n, description: description.trim(), license: license.trim(), compatibility: compatibility.trim() });
  };
  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title size="4">新建技能</Dialog.Title>
        <Dialog.Description size="1" color="gray">技能是 agent 可调用的能力包；保存后在服务端的 skills/ 目录生成骨架。</Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          <Field label="名称"><TextField.Root value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 web-recon" /></Field>
          <Field label="描述"><TextArea value={description} onChange={(e) => setDescription(e.target.value)} placeholder="一句话说明这个技能是干什么的" rows={3} /></Field>
          <Flex gap="3">
            <Field label="许可（可选）"><TextField.Root value={license} onChange={(e) => setLicense(e.target.value)} placeholder="例如 MIT" /></Field>
            <Field label="兼容性（可选）"><TextField.Root value={compatibility} onChange={(e) => setCompatibility(e.target.value)} placeholder="例如 artex-harness" /></Field>
          </Flex>
          {err ? <Text size="1" color="red">{err}</Text> : null}
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close><Button variant="soft" color="gray">取消</Button></Dialog.Close>
          <Button onClick={submit}>保存</Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}
