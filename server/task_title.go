package server

// 会话标题：任务创建后异步让模型起一个短标题，写进 tasks.name。
//
// 不引库的理由：项目已经有 LLM 通道（taskLLMRuntime，带重试与多 profile 故障转移），
// 标题只是一次极短的非流式调用。再引一个「标题生成」库，等于把已有的重试/鉴权/代理
// 全部绕开重做一遍。
//
// 约束：
//   - 只在该任务 name 为空时生成一次（db.SetTaskName 里的 WHERE 也保证了这点）；
//   - 失败只记日志，不影响任务本身；
//   - 有超时，不会因为模型慢而挂住创建请求（调用方是 goroutine）。

import (
	"context"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/norma/llm"
)

const (
	titleMaxRunes  = 24
	titlePromptSys = "你是任务标题生成器。只输出一个不超过 16 个汉字（或 24 个字符）的短标题，" +
		"概括这次会话要做什么。不要标点、不要引号、不要解释、不要重复用户原话的完整句子。"
)

// ensureTaskTitle generates a title once. Safe to call in a goroutine.
func (s *Server) ensureTaskTitle(ctx context.Context, t *Task) {
	if t == nil || strings.TrimSpace(t.Name) != "" {
		return
	}
	goal := strings.TrimSpace(t.Goal)
	if goal == "" {
		goal = strings.TrimSpace(t.Description)
	}
	if goal == "" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	rt := &taskLLMRuntime{s: s, taskID: t.ID, agentKey: "harness"}
	msg, _, _, err := rt.Complete(ctx, llm.CompletionRequest{
		System:    []string{titlePromptSys},
		Messages:  []llm.Message{llm.UserText("任务：" + clipRunes(goal, 400))},
		MaxTokens: 48,
	})
	if err != nil {
		log.Printf("[title] task %s 生成标题失败（忽略）: %v", t.ID, err)
		return
	}
	title := sanitizeTitle(messageText(msg))
	if title == "" {
		return
	}
	if err := s.m.pg.SetTaskName(taskIDInt(t.ID), title); err != nil {
		log.Printf("[title] task %s 写入标题失败: %v", t.ID, err)
		return
	}
	// 同步内存里的任务对象：列表接口读的是 manager 的缓存，只写库不会立刻可见。
	t.Name = title
	log.Printf("[title] task %s → %q", t.ID, title)
}

// messageText returns the first text block of a message.
func messageText(m llm.Message) string {
	for _, b := range m.Content {
		if b.Type == llm.BlockText && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}

// sanitizeTitle strips the punctuation and ceremony models like to add
// ("标题：…", quotes, trailing periods) and trims to the display budget.
func sanitizeTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "标题：")
	s = strings.TrimPrefix(s, "标题:")
	s = strings.Trim(s, " \"'`《》「」“”‘’")
	s = strings.TrimRight(s, "。.!！?？,，;；")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	return clipRunes(s, titleMaxRunes)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
