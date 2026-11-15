import { useEffect, useState } from "react";
import {
  Badge, Box, Button, Card, Code, Flex, Heading, IconButton, ScrollArea, Separator,
  Table, Text, TextField, Tooltip,
} from "@radix-ui/themes";
import { ArrowLeft, Check, Cpu, FileText, Palette, Plug, Shield, Sparkles, Terminal, Trash2 } from "lucide-react";

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

export default function SettingsView({ onBack, appearance, onAppearance, version, health, toast }) {
  const [section, setSection] = useState("models");
  const [profiles, setProfiles] = useState([]);
  const [mcps, setMcps] = useState([]);
  const [skills, setSkills] = useState([]);
  const [rules, setRules] = useState([]);
  const [logs, setLogs] = useState([]);
  const [err, setErr] = useState("");

  useEffect(() => {
    jfetch("/api/llm/profiles").then((d) => setProfiles(Array.isArray(d) ? d : d.profiles || [])).catch((e) => setErr(e.message));
    jfetch("/api/mcp").then((d) => setMcps(Array.isArray(d) ? d : d.servers || [])).catch(() => {});
    jfetch("/api/skills").then((d) => setSkills(Array.isArray(d) ? d : d.skills || [])).catch(() => {});
    jfetch("/api/intercept/rules").then((d) => setRules(Array.isArray(d) ? d : d.rules || [])).catch(() => {});
  }, []);
  useEffect(() => {
    if (section === "logs" && !logs.length) jfetch("/api/llm/records").then((d) => setLogs(Array.isArray(d) ? d : d.records || [])).catch(() => {});
  }, [section]); // eslint-disable-line react-hooks/exhaustive-deps

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
    <div className="app settings-app">
      {/* 左栏：设置导航（取代会话列表） */}
      <aside className="rail settings-rail" aria-label="设置导航">
        <Flex align="center" gap="2" px="3" pt="3" pb="2">
          <Tooltip content="返回会话">
            <IconButton size="1" variant="ghost" color="gray" aria-label="返回会话" onClick={onBack}><ArrowLeft size={15} /></IconButton>
          </Tooltip>
          <Heading size="3" weight="medium">设置</Heading>
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

      {/* 主区：分区表单 */}
      <div className="main settings-main">
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
                {!providers.length ? <Text size="1" color="gray">还没有供应商。供应商与模型的新增/编辑沿用旧设置入口的接口（POST /api/llm/profiles）。</Text> : null}
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
