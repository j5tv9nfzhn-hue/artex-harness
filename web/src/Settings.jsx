import { useEffect, useState } from "react";
import {
  Badge, Box, Code, Dialog, Flex, Separator, Table, Tabs, Text, IconButton, Select, Tooltip,
} from "@radix-ui/themes";
import { Check, Cpu, Palette, Plug, Sparkles } from "lucide-react";

/* 设置页：只用后端已有的接口，每一项都是真实数据。
 *   模型   GET /api/llm/profiles + POST /api/llm/profiles/active
 *   MCP    GET /api/mcp（名称/传输/启用/tools 缓存数）
 *   技能   GET /api/skills
 *   外观   Radix Theme 的 appearance（客户端偏好）
 *   ？    没有接口的能力不出现，不留空壳标签页。
 */

const authHeaders = () => {
  let t = ""; try { t = localStorage.getItem("artex.harness.token") || ""; } catch {}
  return t ? { Authorization: "Bearer " + t } : {};
};
const get = (p) => fetch(p, { credentials: "include", headers: authHeaders() }).then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.status + ""))));

export default function Settings({ open, onOpenChange, appearance, onAppearance, version, toast }) {
  const [profiles, setProfiles] = useState([]);
  const [mcps, setMcps] = useState([]);
  const [skills, setSkills] = useState([]);
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!open) return;
    setErr("");
    get("/api/llm/profiles").then((d) => setProfiles(Array.isArray(d) ? d : d.profiles || [])).catch((e) => setErr("模型列表加载失败：" + e.message));
    get("/api/mcp").then((d) => setMcps(Array.isArray(d) ? d : d.servers || [])).catch(() => {});
    get("/api/skills").then((d) => setSkills(Array.isArray(d) ? d : d.skills || [])).catch(() => {});
  }, [open]);

  async function activate(id) {
    try {
      const r = await fetch("/api/llm/profiles/active", {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ id: Number(id) }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 140));
      setProfiles((prev) => prev.map((p) => ({ ...p, is_default: String(p.id) === String(id) })));
      toast?.("已切换默认模型");
    } catch (e) { toast?.(e.message, true); }
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Content maxWidth="720px">
        <Dialog.Title size="5">设置</Dialog.Title>
        <Dialog.Description size="1" color="gray">
          artex-harness {version ? "v" + version : ""}
        </Dialog.Description>

        {err ? <Box mt="3"><Text size="1" color="red">{err}</Text></Box> : null}

        <Tabs.Root defaultValue="models" mt="3">
          <Tabs.List>
            <Tabs.Trigger value="models"><Cpu size={13} /> 模型</Tabs.Trigger>
            <Tabs.Trigger value="mcp"><Plug size={13} /> MCP</Tabs.Trigger>
            <Tabs.Trigger value="skills"><Sparkles size={13} /> 技能</Tabs.Trigger>
            <Tabs.Trigger value="look"><Palette size={13} /> 外观</Tabs.Trigger>
          </Tabs.List>

          <Box pt="3">
            <Tabs.Content value="models">
              <Table.Root size="1" variant="surface">
                <Table.Header>
                  <Table.Row><Table.ColumnHeaderCell>名称</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>模型</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>上下文</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell /></Table.Row>
                </Table.Header>
                <Table.Body>
                  {profiles.map((p) => (
                    <Table.Row key={p.id} align="center">
                      <Table.Cell>{p.name || "—"}</Table.Cell>
                      <Table.Cell><Code size="1">{p.model}</Code></Table.Cell>
                      <Table.Cell>{p.context_window_k ? p.context_window_k + "K" : "默认"}</Table.Cell>
                      <Table.Cell>
                        {p.is_default
                          ? <Badge size="1" color="green" variant="soft">默认</Badge>
                          : <IconButton size="1" variant="soft" onClick={() => activate(p.id)} aria-label="设为默认"><Check size={12} /></IconButton>}
                      </Table.Cell>
                    </Table.Row>
                  ))}
                  {!profiles.length ? <Table.Row><Table.Cell colSpan={4}><Text size="1" color="gray">未配置模型</Text></Table.Cell></Table.Row> : null}
                </Table.Body>
              </Table.Root>
            </Tabs.Content>

            <Tabs.Content value="mcp">
              <Flex direction="column" gap="2">
                {mcps.map((m) => (
                  <Flex key={m.id || m.name} align="center" gap="2">
                    <Text size="2" style={{ minWidth: 140 }}>{m.name}</Text>
                    <Code size="1">{m.transport || "—"}</Code>
                    <Badge size="1" variant="soft" color={m.enabled ? "green" : "gray"}>{m.enabled ? "启用" : "停用"}</Badge>
                    {typeof m.tools_cached === "number" ? <Text size="1" color="gray">{m.tools_cached} 个工具</Text> : null}
                  </Flex>
                ))}
                {!mcps.length ? <Text size="1" color="gray">没有配置 MCP 服务</Text> : null}
              </Flex>
            </Tabs.Content>

            <Tabs.Content value="skills">
              <Flex wrap="wrap" gap="2">
                {(skills || []).map((s) => (
                  <Badge key={s.name || s} size="1" variant="soft">{s.name || s}</Badge>
                ))}
                {!skills.length ? <Text size="1" color="gray">没有可用技能</Text> : null}
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
      </Dialog.Content>
    </Dialog.Root>
  );
}
