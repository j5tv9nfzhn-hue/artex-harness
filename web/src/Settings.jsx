import { useEffect, useMemo, useState } from "react";
import {
  Badge, Box, Button, Card, Code, Dialog, Flex, IconButton, Select, Separator, Tabs,
  Table, Text, TextField, Tooltip,
} from "@radix-ui/themes";
import { Check, Cpu, FileText, Palette, Pencil, Plug, Plus, Sparkles, Trash2 } from "lucide-react";

/* 设置页：供应商 → 模型 两级。
 *
 * 后端没有独立的 provider 实体：一个 profile 就带有 format(供应商类型) + base_url +
 * proxy + api_key + model。因此界面上把 (format, base_url) 相同的 profile 归为同一
 * 个供应商，其下的 profile 就是这家供应商的模型。
 *
 *   供应商卡片  format / base_url / key 提示 / proxy / 是否含默认模型
 *   模型行      model / 上下文 / 思考设置；可设为默认、删除
 *   新增供应商  format + base_url + key + proxy + 首个模型 → POST /api/llm/profiles
 *   新增模型    在同一供应商下追加一个 model → POST /api/llm/profiles
 */

const authHeaders = () => {
  let t = ""; try { t = localStorage.getItem("artex.harness.token") || ""; } catch {}
  return t ? { Authorization: "Bearer " + t } : {};
};
const jfetch = (p, opts = {}) =>
  fetch(p, { credentials: "include", ...opts, headers: { ...authHeaders(), ...(opts.body ? { "Content-Type": "application/json" } : {}), ...(opts.headers || {}) } })
    .then(async (r) => { if (!r.ok) throw new Error((await r.text()).slice(0, 180)); return r.headers.get("content-type")?.includes("json") ? r.json() : null; });

const FORMATS = [
  { value: "openai", label: "OpenAI 兼容（模型服务、vLLM、Ollama…）" },
  { value: "anthropic", label: "Anthropic" },
  { value: "openai-responses", label: "OpenAI Responses" },
];
const fmtLabel = (f) => (FORMATS.find((x) => x.value === f)?.label || f || "—");

export default function Settings({ open, onOpenChange, appearance, onAppearance, version, toast }) {
  const [profiles, setProfiles] = useState([]);
  const [mcps, setMcps] = useState([]);
  const [skills, setSkills] = useState([]);
  const [adding, setAdding] = useState(null); // {provider?} | "new"
  const [editing, setEditing] = useState(null); // 正在编辑的模型 profile
  const [editProvider, setEditProvider] = useState(null); // 正在编辑的供应商分组
  const [logs, setLogs] = useState([]);
  const [err, setErr] = useState("");

  async function reload() {
    try { const d = await jfetch("/api/llm/profiles"); setProfiles(Array.isArray(d) ? d : d.profiles || []); }
    catch (e) { setErr("模型配置加载失败：" + e.message); }
  }
  useEffect(() => {
    if (!open) return;
    setErr(""); reload();
    jfetch("/api/mcp").then((d) => setMcps(Array.isArray(d) ? d : d.servers || [])).catch(() => {});
    jfetch("/api/skills").then((d) => setSkills(Array.isArray(d) ? d : d.skills || [])).catch(() => {});
  }, [open]);

  // 按 (format, base_url) 归组 = 供应商
  const providers = useMemo(() => {
    const map = new Map();
    for (const p of profiles) {
      const key = `${p.format || "openai"}|${p.base_url || ""}|${p.proxy || ""}`;
      if (!map.has(key)) map.set(key, { key, format: p.format, base_url: p.base_url, proxy: p.proxy, api_key_hint: p.api_key_hint, models: [] });
      map.get(key).models.push(p);
    }
    return [...map.values()];
  }, [profiles]);

  async function activate(id) {
    try { await jfetch("/api/llm/profiles/active", { method: "POST", body: JSON.stringify({ id: Number(id) }) });
      toast?.("已切换默认模型"); reload(); } catch (e) { toast?.(e.message, true); }
  }
  async function remove(p) {
    try { await jfetch(`/api/llm/profiles/${p.id}`, { method: "DELETE" }); toast?.("已删除 " + (p.model || p.name)); reload(); }
    catch (e) { toast?.(e.message, true); }
  }
  async function save(body) {
    try { await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify(body) });
      toast?.("已保存"); setAdding(null); reload(); } catch (e) { toast?.(e.message, true); }
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Content maxWidth="840px">
        <Dialog.Title size="5">设置</Dialog.Title>
        <Dialog.Description size="1" color="gray">artex-harness {version ? "v" + version : ""}</Dialog.Description>
        {err ? <Box mt="2"><Text size="1" color="red">{err}</Text></Box> : null}

        <Tabs.Root defaultValue="models" mt="3">
          <Tabs.List>
            <Tabs.Trigger value="models"><Cpu size={13} /> 供应商与模型</Tabs.Trigger>
            <Tabs.Trigger value="mcp"><Plug size={13} /> MCP</Tabs.Trigger>
            <Tabs.Trigger value="skills"><Sparkles size={13} /> 技能</Tabs.Trigger>
            <Tabs.Trigger value="logs"><FileText size={13} /> 会话日志</Tabs.Trigger>
            <Tabs.Trigger value="look"><Palette size={13} /> 外观</Tabs.Trigger>
          </Tabs.List>

          <Box pt="3">
            <Tabs.Content value="models">
              <Flex direction="column" gap="3">
                {providers.map((pv) => (
                  <Card key={pv.key} size="1">
                    <Flex direction="column" gap="2">
                      <Flex align="center" gap="2" wrap="wrap">
                        <Text size="2" weight="medium">{fmtLabel(pv.format)}</Text>
                        <Code size="1">{pv.base_url || "默认端点"}</Code>
                        {pv.api_key_hint ? <Badge size="1" variant="soft" color="green">密钥 {pv.api_key_hint}</Badge> : <Badge size="1" variant="soft" color="red">缺少密钥</Badge>}
                        {pv.proxy ? <Badge size="1" variant="soft" color="gray">代理 {pv.proxy}</Badge> : null}
                        <Box style={{ marginLeft: "auto" }}>
                          <Flex gap="1">
                            <Button size="1" variant="soft" color="gray" onClick={() => setEditProvider(pv)}>
                              <Pencil size={12} /> 端点
                            </Button>
                            <Button size="1" variant="soft" onClick={() => setAdding({ provider: pv })}>
                              <Plus size={12} /> 模型
                            </Button>
                          </Flex>
                        </Box>
                      </Flex>
                      <Separator size="4" />
                      <Table.Root size="1" variant="ghost">
                        <Table.Header>
                          <Table.Row>
                            <Table.ColumnHeaderCell>模型</Table.ColumnHeaderCell>
                            <Table.ColumnHeaderCell>上下文</Table.ColumnHeaderCell>
                            <Table.ColumnHeaderCell>思考</Table.ColumnHeaderCell>
                            <Table.ColumnHeaderCell>速率</Table.ColumnHeaderCell>
                            <Table.ColumnHeaderCell />
                          </Table.Row>
                        </Table.Header>
                        <Table.Body>
                          {pv.models.map((m) => (
                            <Table.Row key={m.id} align="center">
                              <Table.Cell>
                                <Flex align="center" gap="2">
                                  <Text size="1">{m.model}</Text>
                                  {m.is_default ? <Badge size="1" color="green" variant="soft">默认</Badge> : null}
                                </Flex>
                              </Table.Cell>
                              <Table.Cell><Text size="1" color="gray">{m.context_window_k ? m.context_window_k + "K" : "默认"}</Text></Table.Cell>
                              <Table.Cell><Text size="1" color="gray">{[m.thinking_type, m.reasoning_effort].filter(Boolean).join(" / ") || "默认"}</Text></Table.Cell>
                              <Table.Cell><Text size="1" color="gray">{m.rate_per_second ? m.rate_per_second + "/s" : m.rate_per_minute ? m.rate_per_minute + "/min" : "不限"}</Text></Table.Cell>
                              <Table.Cell>
                                <Flex gap="1" justify="end">
                                  <Tooltip content="编辑该模型">
                                    <IconButton size="1" variant="ghost" aria-label="编辑" onClick={() => setEditing(m)}><Pencil size={12} /></IconButton>
                                  </Tooltip>
                                  {m.is_default ? null : (
                                    <Tooltip content="设为默认模型">
                                      <IconButton size="1" variant="ghost" aria-label="设为默认" onClick={() => activate(m.id)}><Check size={12} /></IconButton>
                                    </Tooltip>
                                  )}
                                  <Tooltip content="删除该模型">
                                    <IconButton size="1" variant="ghost" color="red" aria-label="删除" onClick={() => remove(m)}><Trash2 size={12} /></IconButton>
                                  </Tooltip>
                                </Flex>
                              </Table.Cell>
                            </Table.Row>
                          ))}
                        </Table.Body>
                      </Table.Root>
                    </Flex>
                  </Card>
                ))}

                {!providers.length ? <Text size="1" color="gray">还没有任何供应商。先添加一个供应商，再为它添加模型。</Text> : null}

                <Button size="2" variant="soft" onClick={() => setAdding("new")}>
                  <Plus size={13} /> 添加供应商
                </Button>
              </Flex>
            </Tabs.Content>

            <Tabs.Content value="mcp">
              <Flex direction="column" gap="2">
                {mcps.map((m) => (
                  <Flex key={m.id || m.name} align="center" gap="2">
                    <Text size="2" style={{ minWidth: 150 }}>{m.name}</Text>
                    <Code size="1">{m.transport || "—"}</Code>
                    <Badge size="1" variant="soft" color={m.enabled ? "green" : "gray"}>{m.enabled ? "启用" : "停用"}</Badge>
                  </Flex>
                ))}
                {!mcps.length ? <Text size="1" color="gray">没有配置 MCP 服务</Text> : null}
              </Flex>
            </Tabs.Content>

            <Tabs.Content value="skills">
              <Flex wrap="wrap" gap="2">
                {skills.map((s) => <Badge key={s.name || s} size="1" variant="soft">{s.name || s}</Badge>)}
                {!skills.length ? <Text size="1" color="gray">没有可用技能</Text> : null}
              </Flex>
            </Tabs.Content>

            <Tabs.Content value="logs">
              <Flex direction="column" gap="2">
                <Flex justify="between" align="center">
                  <Text size="1" color="gray">最近的模型调用记录（GET /api/llm/records）</Text>
                  <Button size="1" variant="soft" onClick={() => jfetch("/api/llm/records").then((d) => setLogs(Array.isArray(d) ? d : d.records || d.items || [])).catch((e) => toast?.(e.message, true))}>刷新</Button>
                </Flex>
                <Table.Root size="1" variant="surface">
                  <Table.Header><Table.Row>
                    <Table.ColumnHeaderCell>时间</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>模型</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>输入</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>输出</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>状态</Table.ColumnHeaderCell>
                  </Table.Row></Table.Header>
                  <Table.Body>
                    {logs.slice(0, 50).map((r, i) => (
                      <Table.Row key={r.id || i} align="center">
                        <Table.Cell><Text size="1" color="gray">{(r.created_at || "").replace("T", " ").slice(0, 19)}</Text></Table.Cell>
                        <Table.Cell><Code size="1">{r.model || "—"}</Code></Table.Cell>
                        <Table.Cell><Text size="1">{r.input_tokens ?? "—"}</Text></Table.Cell>
                        <Table.Cell><Text size="1">{r.output_tokens ?? "—"}</Text></Table.Cell>
                        <Table.Cell><Badge size="1" variant="soft" color={r.status === "ok" ? "green" : "red"}>{r.status || "—"}</Badge></Table.Cell>
                      </Table.Row>
                    ))}
                    {!logs.length ? <Table.Row><Table.Cell colSpan={5}><Text size="1" color="gray">点「刷新」加载调用记录</Text></Table.Cell></Table.Row> : null}
                  </Table.Body>
                </Table.Root>
              </Flex>
            </Tabs.Content>

            <Tabs.Content value="look">
              <Flex align="center" gap="2">
                <Text size="2">主题</Text>
                <Select.Root value={appearance} onValueChange={onAppearance}>
                  <Select.Trigger />
                  <Select.Content>
                    <Select.Item value="inherit">跟随系统</Select.Item>
                    <Select.Item value="light">浅色</Select.Item>
                    <Select.Item value="dark">深色</Select.Item>
                  </Select.Content>
                </Select.Root>
              </Flex>
            </Tabs.Content>
          </Box>
        </Tabs.Root>

        <EditModelDialog model={editing} onClose={() => setEditing(null)}
          onSave={async (body) => { await save(body); setEditing(null); }} />
        <EditProviderDialog pv={editProvider} onClose={() => setEditProvider(null)}
          onSave={async (patch) => {
            try {
              for (const m of editProvider.models) {
                await jfetch("/api/llm/profiles", { method: "POST", body: JSON.stringify({ ...m, api_key: patch.api_key || "", base_url: patch.base_url, proxy: patch.proxy }) });
              }
              toast?.("供应商已更新"); setEditProvider(null); reload();
            } catch (e) { toast?.(e.message, true); }
          }} />

        <ProviderForm
          open={!!adding}
          seed={adding && adding !== "new" ? adding.provider : null}
          onClose={() => setAdding(null)}
          onSave={save}
        />
      </Dialog.Content>
    </Dialog.Root>
  );
}

/* 新增供应商 / 新增模型：新增供应商时填端点与密钥；新增模型时端点沿用所属供应商。 */
function ProviderForm({ open, seed, onClose, onSave }) {
  const isNew = !seed;
  const [format, setFormat] = useState("openai");
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [proxy, setProxy] = useState("");
  const [model, setModel] = useState("");
  const [ctx, setCtx] = useState("");
  const [thinking, setThinking] = useState("");
  const [effort, setEffort] = useState("");

  useEffect(() => {
    if (!open) return;
    setFormat(seed?.format || "openai");
    setBaseURL(seed?.base_url || "");
    setProxy(seed?.proxy || "");
    setApiKey(""); setModel(""); setCtx(""); setThinking(""); setEffort("");
  }, [open, seed]);

  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title size="4">{isNew ? "添加供应商" : "添加模型"}</Dialog.Title>
        <Dialog.Description size="1" color="gray">
          {isNew ? "先填供应商端点与密钥，以及它的第一个模型。" : "端点与密钥沿用该供应商，只需填模型名与参数。"}
        </Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          {isNew ? (
            <>
              <Field label="供应商类型">
                <Select.Root value={format} onValueChange={setFormat}>
                  <Select.Trigger style={{ width: "100%" }} />
                  <Select.Content>{FORMATS.map((f) => <Select.Item key={f.value} value={f.value}>{f.label}</Select.Item>)}</Select.Content>
                </Select.Root>
              </Field>
              <Field label="Base URL"><TextField.Root value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://…/v1" /></Field>
              <Field label="API Key"><TextField.Root type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="sk-…" /></Field>
              <Field label="出站代理（可选）"><TextField.Root value={proxy} onChange={(e) => setProxy(e.target.value)} placeholder="http://127.0.0.1:3128" /></Field>
            </>
          ) : (
            <Field label="供应商"><Code size="2">{seed.base_url || "默认端点"}</Code></Field>
          )}
          <Field label="模型名"><TextField.Root value={model} onChange={(e) => setModel(e.target.value)} placeholder="例如 MiniMax-M3.1-Flash" /></Field>
          <Flex gap="3">
            <Field label="上下文 K（可选）"><TextField.Root value={ctx} onChange={(e) => setCtx(e.target.value)} placeholder="128" /></Field>
            <Field label="思考开关（可选）">
              <Select.Root value={thinking || "none"} onValueChange={(v) => setThinking(v === "none" ? "" : v)}>
                <Select.Trigger style={{ width: "100%" }} />
                <Select.Content>
                  <Select.Item value="none">不发送</Select.Item>
                  <Select.Item value="enabled">开启</Select.Item>
                  <Select.Item value="disabled">关闭</Select.Item>
                </Select.Content>
              </Select.Root>
            </Field>
            <Field label="思考强度（可选）">
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
          <Button onClick={() => onSave({
            name: model || "default",
            format, base_url: baseURL, proxy, api_key: apiKey,
            model, context_window_k: Number(ctx) || 0,
            thinking_type: thinking, reasoning_effort: effort,
          })}>保存</Button>
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

function EditModelDialog({ model, onClose, onSave }) {
  const [modelName, setModelName] = useState("");
  const [ctx, setCtx] = useState("");
  const [thinking, setThinking] = useState("");
  const [effort, setEffort] = useState("");
  const [rps, setRps] = useState("");
  const [rpm, setRpm] = useState("");

  useEffect(() => {
    if (!model) return;
    setModelName(model.model || "");
    setCtx(model.context_window_k ? String(model.context_window_k) : "");
    setThinking(model.thinking_type || "");
    setEffort(model.reasoning_effort || "");
    setRps(model.rate_per_second ? String(model.rate_per_second) : "");
    setRpm(model.rate_per_minute ? String(model.rate_per_minute) : "");
  }, [model]);

  return (
    <Dialog.Root open={!!model} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title size="4">编辑模型</Dialog.Title>
        <Dialog.Description size="1" color="gray">
          密钥留空即保留原值。
        </Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          <Field label="模型名"><TextField.Root value={modelName} onChange={(e) => setModelName(e.target.value)} /></Field>
          <Flex gap="3">
            <Field label="上下文 K"><TextField.Root value={ctx} onChange={(e) => setCtx(e.target.value)} placeholder="128" /></Field>
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
          <Button onClick={() => onSave({
            ...model,
            model: modelName, context_window_k: Number(ctx) || 0,
            thinking_type: thinking, reasoning_effort: effort,
            rate_per_second: Number(rps) || 0, rate_per_minute: Number(rpm) || 0,
          })}>保存</Button>
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
        <Dialog.Description size="1" color="gray">
          改动会应用到该供应商下的 {pv?.models?.length || 0} 个模型。密钥留空即保留原值。
        </Dialog.Description>
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
