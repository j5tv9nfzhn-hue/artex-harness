package server

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	actool "github.com/Autumn-27/norma/tool"
)

// TestGitBashSettingsPersistence 覆盖 Git Bash 路径配置的核心链路：写入库 →
// applyGitBash 推给执行层 → shellStatus 回显一致。
//
// 2026-11-21 S6-TRIM 改写：GET/PUT /api/settings 已删，原测试走 HTTP 设置面板
// 的部分随之失效。非法输入的 400 校验与引号清洗都在已删的 handler 里，
// validateGitBashPath 纯函数也不在了，这两类断言无法保留；保留下来的断言只
// 覆盖还存在的层（库 + applyGitBash + shellStatus）。
func TestGitBashSettingsPersistence(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("database unavailable (%v) — skipping", err)
	}
	if m.pg == nil {
		t.Skip("postgres unavailable — skipping")
	}
	t.Cleanup(func() { actool.SetShellPath("") })
	s := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())

	// 结构存在且字段齐备。
	status := s.shellStatus()
	for _, k := range []string{"applicable", "platform", "configured", "path", "family", "source"} {
		if _, has := status[k]; !has {
			t.Errorf("git_bash 缺字段 %q", k)
		}
	}
	if status["platform"] != runtime.GOOS {
		t.Errorf("platform=%v, want %v", status["platform"], runtime.GOOS)
	}
	if status["applicable"] != (runtime.GOOS == "windows") {
		t.Errorf("applicable=%v, 应与平台一致", status["applicable"])
	}
	// 解析结果必须有值：即使什么都没配，也应回显当前真正在用的 shell 与来源，
	// 否则操作者无从判断"配置到底生效了没有"。
	if p, _ := status["path"].(string); p == "" {
		t.Error("git_bash.path 为空：执行层必须报告当前生效的 shell")
	}
	if src, _ := status["source"].(string); src == "" {
		t.Error("git_bash.source 为空：无法判断配置是否真的生效")
	}

	// 合法绝对路径：写入库 → applyGitBash → 回显与执行层拿到同一个值（即时生效，无需重启）。
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on this host")
	}
	bashPath, _ = filepath.Abs(bashPath)

	if err := m.pg.SetSetting(settingGitBashPath, bashPath); err != nil {
		t.Fatalf("保存合法路径失败: %v", err)
	}
	s.applyGitBash()
	if got := s.shellStatus()["configured"]; got != bashPath {
		t.Fatalf("configured=%v, want %v", got, bashPath)
	}
	if got := waitShellPath(bashPath); got != bashPath {
		t.Fatalf("执行层未收到保存的路径: got=%q want=%q（保存后必须即时推送到 actool）", got, bashPath)
	}

	// 空串 = 清空回自动探测（与 python_interpreter 的语义一致）。
	if err := m.pg.SetSetting(settingGitBashPath, ""); err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	s.applyGitBash()
	if got := s.shellStatus()["configured"]; got != "" {
		t.Fatalf("清空后 configured=%v, want 空串", got)
	}
	if got := waitShellPath(""); got != "" {
		t.Fatalf("清空未传导到执行层: %q", got)
	}
}

// waitShellPath 轮询执行层的显式路径设置。开机 applyGitBash 跑在启动协程里，
// 保存动作是另一条路径，两者都从同一个库读同一个值，所以只断言终态一致，
// 不假设先后顺序。
func waitShellPath(want string) string {
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := actool.ShellPath()
		if got == want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestGitBashDetect 是"一键检测"端点：带 path 走手动验证，不带走自动探测。
// 断言只针对两端共有的结构，避免依赖具体机器的安装情况。

// 自动检测。

// 手动路径实测：真 bash 应通过并带出版本行。

// 不存在的路径：结论必须是不可用并附原因（而不是 500）。

// 普通可执行文件也要被拒绝：存在性检查会放行，语法检查不会。

// TestGitBashBrowseIsWindowsOnlyAndPathGuarded 检查浏览端点的安全边界：
// 非 Windows 一律拒绝；Windows 上只接受不含 .. 的绝对目录。

// 相对路径与 .. 越界都要拒绝。

// TestValidateGitBashPath 纯函数校验：引号清洗、相对路径拒绝、目录拒绝。
