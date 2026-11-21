# S6-REFUSAL：模型拒答兜底（2026-11-21，本仓库新增，非上游）

Julie 确认“发现列表页 + 拒答兜底两件一起处理”，本文是拒答兜底一件。只动后端与评测，不改前端；不改系统提示词本身、不动 guard 硬拒绝逻辑、不动 `third_party/`。

## 现状：拒答为什么 previously 无兜底

- `llmpool.Pool.Complete/Stream` 的 `shouldFailover` 只认传输/5xx/429 类错误；内容拒答是 HTTP 200 + 拒答文本、无工具调用，会当作一次成功返回。
- 上游 norma 的 OpenAI 适配器已把结构化 `refusal` 字段转成普通 TextBlock（`third_party/norma/llm/openai.go:469-473`，流式 548-550），所以 llmpool 层只能看到纯文本——检测必须是基于文本的。
- 子代理拒答只记为普通 `failed`（`agent/harness.go` 的 `finishDelegation`，`res.IsError || err != nil` 即 `StatusFailed`），且多数情况下拒答是以成功的文本结果返回的，连 failed 都不是——直接淹没。

## 设计决策

### 1. 检测阈值：宁漏勿误判

`llmpool/refusal.go` 的 `Detect(msg llm.Message) Result`，结构化返回是否拒答 + 命中信号 + 原文摘录。

- 只在“无工具调用的纯文本回复”上判定（`len(msg.ToolUses()) == 0` 且文本非空）。调了工具就是干了活，哪怕文本里引用了拒答也不算。
- 强信号才算，中英文：英文 `I can't/cannot help/assist`、`unable to help/assist/comply`、`not allowed to`、`I must/have to refuse`、`sorry but I can't/cannot`；中文 `我无法协助/我不能帮助/协助/帮你/我无法提供帮助/不被允许/我不能（无法）执行`。每条信号在代码里逐条注释了为什么算强信号。
- 刻意不收的信号：裸 `cannot/无法`（正常失败文本“cannot connect / 我无法连接目标”里到处都是）、裸 `作为 AI`（只是自我描述，“作为 AI，我可以帮你…”不是拒答；以它开头的真拒答仍会被后面的拒答动词命中）、裸 `sorry`（模型也会为普通失败道歉）。
- 归一化：小写、去撇号、标点转空格、空白折叠；中文另用去空格的紧凑形式匹配（“作为 AI”与“作为AI”等价）。

代价是明确接受的：措辞新颖的拒答可能漏判。漏判的后果只是本次请求没有兜底、拒答如实返回给调用方（与没有这个功能时一样）；误判的后果是烧掉一次重提示 + 最多两次换模型，白白消耗备用配置的调用和上下文。两者不对称，所以阈值偏向宁漏勿误判。

### 2. 上限两次：最多试 3 个配置

`Pool.Complete` 内：首次拒答先重提示（见 3），仍拒答才换模型，最多换两次。两次足够跳出“某一模型/模型家族的拒答姿态”；再多就是在用整条链去撞一个所有模型都会拒绝的请求——那通常说明请求本身确实越界，或提示词需要人工改写，而不是换模型能解决的。重提示的那次同模型重试不计入换模型次数，但它是每请求至多一次的额外调用，所以总调用次数仍有界（`len(order)+1`），不会死循环。

### 3. 为什么不熔断健康分

拒答不是配置坏了：key 没过期、额度没用完、服务没宕机。`Trip` 会把一个好配置冷却 1–30 分钟，让别的任务也跳过它，是对“坏了”的惩罚；`Pass` 则是“这次成功了”，会清零失败计数——拒答没成功，也不该记成功。所以拒答既不 `Trip` 也不 `Pass`，只记一行 `[llmpool-refusal]` 结构化日志（配置名、信号、是否重提示、是否换模型、最终结果）。重提示后成功、或换模型后成功，才对那个成功的配置记 `Pass`。

### 4. 为什么用尽后如实返回原文，而不是返回错误

调用方（agent harness）拿到的是 `llm.Message` + nil error，与模型正常回答无异。理由：

- provider 层如实透传模型回应是它的职责；把“模型说了不”包装成 `ErrExhausted` 会让上层误诊为“配置全坏了”，进而可能触发熔断、告警、重试等针对基础设施故障的反应，全部是错的。
- 不伪造工具调用、不伪造成功发现：拒答文本里没有任何可执行的结论，硬编一个就是造假，违背 harness“只讲真实做到的”纪律。
- 如实返回也让拒答对操作员可见：日志里有 `[llmpool-refusal] … 结果=已耗尽`，评测里能统计到，子代理侧有 `refused` 标记——可见性由这些机制提供，而不是由一个错误值提供。

### 5. 为什么 Stream 不做重派

流式事件一旦 yield 给调用方就无法撤回：文本已经进了对话历史，重发会重复输出、破坏历史。`Pool.Stream` 因此保持原样（只在传输错误的安全窗口内故障转移，与之前一致）。唯一新增的是无侵入的日志：整条流干净结束、无工具调用、且累计文本命中检测时，记一行 `[llmpool-refusal]` 流式拒答日志；拒答的流也不记 `Pass`（没成功的事不记成功），不记 `Trip`。

### 6. 一次性授权重提示

首次拒答时，先对同一模型追加一段提醒再试一次（`llmpool/refusalReminder`，追加到复制后的 `req.System` 末尾——系统段是模型明确看到的任务级指令，比末尾 user 文本更不容易被当成用户输入的一部分）。措辞呼应系统提示词的授权表述（`agent/harness_prompt.go:16`“任务目标与描述是授权的全部范围”），只提醒“本次任务是已获书面授权的渗透测试、目标在授权范围内、请按授权执行”，不改系统提示词本身。只追加一次，且只在首次拒答时做；重提示必须复用同一 member，不重新取 `order()`（轮转会把重试送到另一个配置上去）。重提示后成功直接返回；仍拒答才换模型。

### 7. 子代理拒答标记

`finishDelegation` 处用同一检测判断子代理返回正文。注意拒答通常以**成功的文本结果**返回（子代理的模型只是回答了拒答文本，无错误），所以标记不只看失败路径：正文命中即 `Record.Refused=true`（`harness/delegation/delegation.go`，`json:"refused,omitempty"`，journal 持久化自动带上，重启后仍在）、activity Metadata 写 `refused:true`、Summary 写“子代理拒答：…”、记一行 `[delegation]` 日志。`Status` 本身不变（拒答的失败仍是 `StatusFailed`，拒答的成功仍是 `StatusDone`），前端与统计继续认 Status。

父级循环没有可接的重试/升级点：委派结果以 tool_result 交回给父模型，由父模型自己决定是否换措辞重试或升级——本功能不替它做这个决定，只保证拒答不再静默淹没在普通失败/成功里。

## 落点

| 件 | 文件 |
|---|---|
| 检测 | `llmpool/refusal.go`（+ `llmpool/refusal_test.go`） |
| 重派 + 重提示 | `llmpool/pool.go`（`Pool.Complete` 内；`Pool.Stream` 只加结束时日志） |
| 评测 | `harness/eval/refusal.go`（`MeasureRefusals`，`ProbeModel{Name, Provider}`，输出各模型拒答率 + 信号分布，可打印 `String()`；+ `harness/eval/refusal_test.go`，fake provider，不调真实模型） |
| 子代理标记 | `harness/delegation/delegation.go`（`Record.Refused` + `FinishWithRefused`）、`agent/harness.go`（`finishDelegation`） |
| 日志 | `[llmpool-refusal]`（pool：模型/配置名、信号、是否重提示、是否换模型、最终结果成功/仍拒答/已耗尽；流式：仅记录）；`[delegation] … 子代理拒答` |

## 验证

- `go vet ./...`、`go build ./...` 通过。
- `go test ./... -count=1`（无库，日志 `~/workspace/.tmp/testdb-refusal-nodb.log`）：除已知需真库的 server 11 个测试外全绿（finding_traffic 5、finding_workflow 3、stage2_http 3，均为“未找到数据库配置”）。
- 真库 `go test ./... -count=1 -timeout 25m`（`artex_harness_test`，日志 `~/workspace/.tmp/testdb-refusal-db.log`）：除已知并行抖动 `agent/TestBlackboardToolsReadDirectSources`（PG 死锁 40P01）外全绿，单独 `-count=3` 通过，未修。

## 没做的事

- Stream 的拒答重派：见决策 5，做不到不重复输出，只能日志 + 文档。
- 父级对子代理拒答的自动重试/升级：父循环没有现成的重试点；只做标记 + activity/日志，由父模型自行决定。
- 拒答文本的机器学习/分类器检测：规则信号已够用且可解释、可测试；分类器会引入不可解释的误判。
- 前端展示 `refused`：本次只动后端与评测，前端未改（`Record.Refused` 已进 API 的 JSON，`refused:true` 已进 activity Metadata，前端后续可直接用）。
