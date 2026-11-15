import { useEffect, useState } from "react";
import {
  Badge, Box, Button, Card, Code, Flex, Heading, IconButton, ScrollArea, Separator,
  Dialog, Select, Table, Text, TextField, Tooltip,
} from "@radix-ui/themes";
import { ArrowLeft, Check, Cpu, FileText, Palette, Pencil, Plug, Plus, Shield, Sparkles, Terminal, Trash2 } from "lucide-react";

/* 设置：与 DSH 相同的形态——打开后【左侧栏变成设置导航】，主区显示该分区的表单。
 * 不是弹窗、不是上下堆叠卡片、不是弹窗内的 tab。
 *   左侧 = 分区枚举（供应商 / 插件·MCP / 技能 / 权限 / 会话日志 / 运行环境 / 外观 / 系统）
 *   右侧 = 选中分区的表单
 */

const authHeaders = () => {
  let t = ""; try { t = localStorage.getItem("artex.harness.token") || ""; } catch {}
  return t ? { Authorization: "Bearer " + t } : {};
};
const jfetch = (p, o = {}) =>
  fetch(p, { credentials: "include", ...o, headers: { ...authHeaders(), ...(o.body ? { "Content-Type": "application/json" } : {}) } })
    .then(async (r) => { if (!r.ok) throw new Error((await r.text()).slice(0, 180)); return r.headers.get("content-type")?.includes("json") ? r.json() : null; });

const SECTIONS = [
  { id: "models", label: "供应商与模型", icon: Cpu },
  { id: "mcp", label: "插件 · MCP", icon: Plug },
  { id: "skills", label: "技能", icon: Sparkles },
  { id: "perm", label: "权限与审批", icon: Shield },
  { id: "logs", label: "会话日志", icon: FileText },
  { id: "env", label: "运行环境", icon: Terminal },
  { id: "look", label: "外观", icon: Palette },
];

export default function SettingsView({ open, onOpenChange, appearance, onAppearance, version, health, toast }) {
  const [section, setSection] = useState("models");
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
      toast?.("已切换默认模型");
      const d = await jfetch("/api/llm/profiles"); setProfiles(Array.isArray(d) ? d : d.profiles || []);
    } catch (e) { toast?.(e.message, true); }
  }
  async function remove(p) {
    try { await jfetch(`/api/llm/profiles/${p.id}`, { method: "DELETE" }); toast?.("已删除");
      const d = await jfetch("/api/llm/profiles"); setProfiles(Array.isArray(d) ? d : d.profiles || []);
    } catch (e) { toast?.(e.message, true); }
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
        <Flex align="center" gap="2" px="5" py="3" style={{ borderBottom: "1px solid var(--gray-a5)" }}>
          <Text size="3" weight="medium">{SECTIONS.find((s) => s.id === section)?.label}</Text>
        </Flex>
        <ScrollArea style={{ flex: 1 }}>
          <Box px="5" py="4" style={{ maxWidth: 900 }}>
            {err ? <Card size="1" mb="3"><Text size="1" color="red">{err}</Text></Card> : null}

            {section === "models" && (
              <Flex direction="column" gap="3">
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
                {!providers.length ? <Text size="1" color="gray">还没有供应商。供应商 = 端点（协议 + Base URL + 密钥 + 代理），模型挂在其下。</Text> : null}
              </Flex>
            )}

            {section === "mcp" && (
              <Flex direction="column" gap="2">
                {mcps.map((m) => (
                  <Flex key={m.id || m.name} align="center" gap="3">
                    <Text size="2" style={{ minWidth: 160 }}>{m.name}</Text>
                    <Code size="1">{m.transport || "—"}</Code>
                    <Badge size="1" variant="soft" color={m.enabled ? "green" : "gray"}>{m.enabled ? "启用" : "停用"}</Badge>
                    <Text size="1" color="gray">{m.command || ""}</Text>
                  </Flex>
                ))}
                {!mcps.length ? <Text size="1" color="gray">没有 MCP 服务</Text> : null}
              </Flex>
            )}

            {section === "skills" && (
              <Flex wrap="wrap" gap="2">
                {skills.map((s) => <Badge key={s.name || s} size="1" variant="soft">{s.name || s}</Badge>)}
                {!skills.length ? <Text size="1" color="gray">没有可用技能</Text> : null}
              </Flex>
            )}

            {section === "perm" && (
              <Flex direction="column" gap="2">
                {rules.map((r) => (
                  <Flex key={r.id} align="center" gap="3">
                    <Badge size="1" variant="soft" color={r.action === "deny" ? "red" : r.action === "ask" ? "amber" : "green"}>{r.action || "—"}</Badge>
                    <Text size="1">{r.tool || r.pattern || "—"}</Text>
                    <Text size="1" color="gray">{r.reason || ""}</Text>
                  </Flex>
                ))}
                {!rules.length ? <Text size="1" color="gray">没有自定义拦截规则；平台内置的破坏性命令拦截始终生效，任务级审批门见侧栏铃铛。</Text> : null}
              </Flex>
            )}

            {section === "logs" && (
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
            )}

            {section === "env" && (
              <Flex direction="column" gap="2">
                <Row k="服务" v={health?.service || "artex"} />
                <Row k="版本" v={health?.version || version || "—"} />
                <Row k="监听" v={window.location.host} />
                <Row k="事件协议" v="AG-UI（SSE）" />
                <Row k="单轮墙钟预算" v="20 分钟（HARNESS_RUN_BUDGET）" />
                <Row k="最大轮次" v="40（agents.max_turns）" />
                <Row k="子代理超时" v="10 分钟（HARNESS_SUBAGENT_TIMEOUT）" />
              </Flex>
            )}

            {section === "look" && (
              <Flex align="center" gap="3">
                <Text size="2">主题</Text>
                {["inherit", "light", "dark"].map((v) => (
                  <Button key={v} size="1" variant={appearance === v ? "solid" : "soft"} onClick={() => onAppearance(v)}>
                    {v === "inherit" ? "跟随系统" : v === "light" ? "浅色" : "深色"}
                  </Button>
                ))}
              </Flex>
            )}
          </Box>
        </ScrollArea>
      </div>
      </div>

      <ProviderDialog open={addProvider} onClose={() => setAddProvider(false)}
        onSave={async (b) => { try { await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify(b) }); toast?.("已添加供应商"); setAddProvider(false); reloadProfiles(); } catch (e) { toast?.(e.message, true); } }} />
      <ProviderDialog open={!!addModelFor} seed={addModelFor} onClose={() => setAddModelFor(null)}
        onSave={async (b) => { try { await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify(b) }); toast?.("已添加模型"); setAddModelFor(null); reloadProfiles(); } catch (e) { toast?.(e.message, true); } }} />
      <EditProviderDialog pv={editProvider} onClose={() => setEditProvider(null)}
        onSave={async (patch) => { try {
          for (const m of editProvider.models) await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify({ ...m, api_key: patch.api_key || "", base_url: patch.base_url, proxy: patch.proxy }) });
          toast?.("供应商已更新"); setEditProvider(null); reloadProfiles();
        } catch (e) { toast?.(e.message, true); } }} />
      <EditModelDialog model={editModel} onClose={() => setEditModel(null)}
        onSave={async (b) => { try { await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify(b) }); toast?.("模型已更新"); setEditModel(null); reloadProfiles(); } catch (e) { toast?.(e.message, true); } }} />
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
  const [model, setModel] = useState("");
  const [ctx, setCtx] = useState("");
  useEffect(() => { if (!open) return; setFormat(seed?.format || "openai"); setBaseURL(seed?.base_url || ""); setProxy(seed?.proxy || ""); setAPIKey(""); setModel(""); setCtx(""); }, [open, seed]);
  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title size="4">{isNew ? "添加供应商" : "添加模型"}</Dialog.Title>
        <Dialog.Description size="1" color="gray">{isNew ? "供应商 = 端点与凭据；模型挂在其下。" : `端点沿用 ${seed?.base_url || "默认端点"}，只需填模型。`}</Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          {isNew ? (<>
            <Field label="协议">
              <Select.Root value={format} onValueChange={setFormat}>
                <Select.Trigger style={{ width: "100%" }} />
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
          <Flex gap="3">
            <Field label="模型名"><TextField.Root value={model} onChange={(e) => setModel(e.target.value)} placeholder="例如 MiniMax-M3.1-Flash" /></Field>
            <Field label="上下文 K"><TextField.Root value={ctx} onChange={(e) => setCtx(e.target.value)} placeholder="128" /></Field>
          </Flex>
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close><Button variant="soft" color="gray">取消</Button></Dialog.Close>
          <Button onClick={() => onSave({ name: model || "default", format, base_url: baseURL, proxy, api_key: apiKey, model, context_window_k: Number(ctx) || 0 })}>保存</Button>
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
  useEffect(() => {
    if (!model) return;
    setCtx(model.context_window_k ? String(model.context_window_k) : "");
    setThinking(model.thinking_type || ""); setEffort(model.reasoning_effort || "");
    setRps(model.rate_per_second ? String(model.rate_per_second) : "");
    setRpm(model.rate_per_minute ? String(model.rate_per_minute) : "");
  }, [model]);
  return (
    <Dialog.Root open={!!model} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title size="4">编辑模型 {model?.model}</Dialog.Title>
        <Flex direction="column" gap="3" mt="3">
          <Flex gap="3">
            <Field label="上下文 K"><TextField.Root value={ctx} onChange={(e) => setCtx(e.target.value)} /></Field>
            <Field label="每秒请求"><TextField.Root value={rps} onChange={(e) => setRps(e.target.value)} placeholder="不限" /></Field>
            <Field label="每分钟请求"><TextField.Root value={rpm} onChange={(e) => setRpm(e.target.value)} placeholder="不限" /></Field>
          </Flex>
          <Flex gap="3">
            <Field label="思考开关">
              <Select.Root value={thinking || "none"} onValueChange={(v) => setThinking(v === "none" ? "" : v)}>
                <Select.Trigger style={{ width: "100%" }} />
                <Select.Content>
                  <Select.Item value="none">不发送</Select.Item>
                  <Select.Item value="enabled">开启</Select.Item>
                  <Select.Item value="disabled">关闭</Select.Item>
                </Select.Content>
              </Select.Root>
            </Field>
            <Field label="思考强度">
              <Select.Root value={effort || "none"} onValueChange={(v) => setEffort(v === "none" ? "" : v)}>
                <Select.Trigger style={{ width: "100%" }} />
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
