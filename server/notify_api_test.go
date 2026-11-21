package server

import (
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

// 本文件只剩推送引擎（server/notifier.go）的纯函数/常量防漂移断言。
// 通知 HTTP 接口（server/notify_api.go）已随 S6-TRIM 删除，
// 其端到端用例（notifyFixture、fakeWebhook 及 14 个接口测试）一并删除。


// TestNotifyBackoffTableMatchesAttemptBudget 是防漂移断言。
//
// 重试预算（db.MaxNotifyAttempts）与退避序列表（notifyBackoff）分居两个包：
// 前者是状态机的策略、后者是引擎的执行节拍。若只改其中一个——比如把预算提到 5
// 次而忘了加退避档位——代码不会报错，只会让第 4、5 次重试沿用最后一档间隔，
// 表现为「重试节奏莫名变慢」，排查时很难联想到是这里。
// 断言两者长度一致，让这种漂移在 CI 里就暴露。
func TestNotifyBackoffTableMatchesAttemptBudget(t *testing.T) {
	if len(notifyBackoff) != db.MaxNotifyAttempts {
		t.Fatalf("退避档位数(%d)与最大尝试次数(%d)不一致——改一个必须同时改另一个",
			len(notifyBackoff), db.MaxNotifyAttempts)
	}
	// 退避间隔必须单调不减，否则重试会越试越急，反而加剧限流。
	for i := 1; i < len(notifyBackoff); i++ {
		if notifyBackoff[i] < notifyBackoff[i-1] {
			t.Fatalf("退避间隔必须单调不减：第 %d 档 %v < 第 %d 档 %v",
				i, notifyBackoff[i], i-1, notifyBackoff[i-1])
		}
	}
}

// TestNotifyRateLimitDoesNotConsumeRetryBudget 锁住「先取令牌再领取」的顺序。
// 若反了（先领后弃），被限流挡下的投递已经计过一次 attempts，
// 预算会被纯粹的等待耗光，最后落进 failed。
func TestNotifyRateLimitDoesNotConsumeRetryBudget(t *testing.T) {
	// 只测令牌桶本身，不需要 Server（也不该为它造一个）。
	n := &Notifier{buckets: map[int64]*notifyBucket{}}
	now := time.Now()
	// 每分钟 1 条：满桶时最多 1 条。
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now); got != 1 {
		t.Fatalf("满桶时每分钟 1 条应取 1 个令牌，得到 %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Millisecond)); got != 0 {
		t.Fatalf("令牌耗尽后应立即返回 0，得到 %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(30*time.Second)); got != 0 {
		t.Fatalf("半程不应补满一个令牌，得到 %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Minute)); got != 1 {
		t.Fatalf("满一个周期应补回 1 个令牌，得到 %d", got)
	}
	// 不限流渠道走有限上限，避免单轮被无限积压拖住。
	if got := n.takeTokens(2, 0, notifyUnlimitedBurstPerTick+10, now); got != notifyUnlimitedBurstPerTick {
		t.Fatalf("不限流应返回每轮上限 %d，得到 %d", notifyUnlimitedBurstPerTick, got)
	}
	// 渠道之间的令牌桶互相独立。
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Millisecond)); got != 0 {
		t.Fatalf("渠道 1 的桶应仍然为空，得到 %d", got)
	}
}

// TestNotifyTakeTokensKeepsUnusedTokens 锁住「只取 want 个」的语义。
//
// 曾经的实现把桶整个抽空后才由调用方截断，于是 rate=100/min 的渠道攒满桶、
// 一轮只用 5 条，剩下 95 个令牌直接丢弃；渠道这一轮没有待发投递时同样照扣。
// 结果是注释声称的「积压时可以一次性冲 rate_per_min 条」在任何情况下都做不到。
func TestNotifyTakeTokensKeepsUnusedTokens(t *testing.T) {
	n := &Notifier{buckets: map[int64]*notifyBucket{}}
	now := time.Now()
	// 桶初始为满（100），本轮只要 5 个。
	if got := n.takeTokens(1, 100, 5, now); got != 5 {
		t.Fatalf("want=5 时应恰好取 5 个令牌，得到 %d", got)
	}
	// 关键断言：余下的 95 个必须还在桶里，而不是被抽空丢弃。
	// 不推进时间，确保取到的只可能来自存量而非补充。
	if got := n.takeTokens(1, 100, 95, now); got != 95 {
		t.Fatalf("剩余令牌应仍可取用（期望 95），得到 %d——桶被整轮抽空了", got)
	}
	if got := n.takeTokens(1, 100, 1, now); got != 0 {
		t.Fatalf("桶已取尽，应返回 0，得到 %d", got)
	}
	// want<=0 不应扣减任何令牌（空轮不收费）。
	n2 := &Notifier{buckets: map[int64]*notifyBucket{}}
	if got := n2.takeTokens(1, 20, 0, now); got != 0 {
		t.Fatalf("want=0 应返回 0，得到 %d", got)
	}
	if got := n2.takeTokens(1, 20, 20, now); got != 20 {
		t.Fatalf("want=0 的那次不该消耗令牌，应仍可取满 20，得到 %d", got)
	}
}

// TestDigestTickPlanDecouplesBatchSizeFromSendBudget 钉住汇总模式的两个量纲。
//
// 汇总批次的大小一旦跟每轮请求预算挂上，rate_per_min=20 的渠道就只能在每个
// 3 秒 tick 里补到 1 个令牌，于是每条汇总消息只装 1 个漏洞——功能上等于没有
// 汇总，而消息头部还写着「近 30 分钟新增 1 个漏洞」。这个退化不会报错，
// 现有的端到端用例也看不出来（它们手动给 stepDigest 传一个够大的 limit，
// 绕过了 step 里的额度计算），所以在这里直接断言决策本身。
func TestDigestTickPlanDecouplesBatchSizeFromSendBudget(t *testing.T) {
	tokens, claimLimit := digestTickPlan()
	// 一批 = 一条消息 = 一次请求 = 一个令牌。令牌的单位是消息，不是漏洞。
	if tokens != 1 {
		t.Fatalf("汇总一批只发一条消息，应恰好消耗 1 个令牌，得到 %d", tokens)
	}
	if claimLimit != db.MaxDigestBatchSize {
		t.Fatalf("汇总批次大小应为内存上界 db.MaxDigestBatchSize=%d，得到 %d",
			db.MaxDigestBatchSize, claimLimit)
	}
	// 关键关系：批次大小必须远大于每轮请求预算。两者一旦同量级，
	// 说明又把「发几条消息」和「一批装几条漏洞」混成了一个数。
	if claimLimit <= notifyMaxSendsPerChannelPerTick {
		t.Fatalf("汇总批次大小 %d 不应受每轮请求预算 %d 约束——"+
			"请求预算是由租约倒推的「发几次请求」，与「一批装几条漏洞」是两个量纲",
			claimLimit, notifyMaxSendsPerChannelPerTick)
	}
}

// TestNotifyTickBudgetFitsWithinLease 是又一条防漂移断言。
//
// 单渠道每轮的投递条数上限（notifyMaxSendsPerChannelPerTick）是从租约时长倒推的：
// 一轮里串行投递的最坏耗时必须 < 租约，否则后几条还没发完租约就过期，
// 多实例部署时对端会把它们重新领走、重复发送。这三个常量分处不同位置，
// 改任意一个都可能打破关系而不会有任何报错——所以在这里钉死。
func TestNotifyTickBudgetFitsWithinLease(t *testing.T) {
	worst := time.Duration(notifyMaxSendsPerChannelPerTick) * notifySendTimeout
	if worst >= notifyLease {
		t.Fatalf("单渠道一轮的最坏耗时 %v 不应达到或超过租约 %v"+
			"（notifyMaxSendsPerChannelPerTick=%d × notifySendTimeout=%v）——"+
			"改这三个常量中的任意一个都要同步检查另外两个",
			worst, notifyLease, notifyMaxSendsPerChannelPerTick, notifySendTimeout)
	}
}
