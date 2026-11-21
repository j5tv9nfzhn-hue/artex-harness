package server

// Git Bash 设置：Windows 裸机上给工具调用层一个真正的 POSIX shell。
//
// 库里存路径（`git_bash_path`），开机与保存后由 applyGitBash 推给 norma
// 执行层；非 Windows 平台本设置不生效（norma 仍是 `bash -c`）。
// 检测/浏览 HTTP 口已随 S6-TRIM 删除，只剩路径解析与状态回显。

import (
	"runtime"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
)

const settingGitBashPath = "git_bash_path"

// shellApplicable 报告本平台的工具调用层是否受该设置影响。
func shellApplicable() bool { return runtime.GOOS == "windows" }

// gitBashPath 读已保存的路径；""=自动探测。
func (s *Server) gitBashPath() string {
	v, _, _ := s.m.pg.GetSetting(settingGitBashPath)
	return strings.TrimSpace(v)
}

// applyGitBash 把库里的路径推给执行层。开机调一次，保存后再调一次。
func (s *Server) applyGitBash() {
	actool.SetShellPath(s.gitBashPath())
}

// shellStatus 回显"当前到底在用哪个 shell、以及为什么"。执行层的实时解析结果
// 和库里的配置都给出，操作者才能看出配置是否真的生效（例如路径写错时会显示
// 实际降级到了哪个兜底档）。
func (s *Server) shellStatus() map[string]any {
	sh := actool.CurrentShell()
	return map[string]any{
		"applicable": shellApplicable(),
		"platform":   runtime.GOOS,
		"configured": s.gitBashPath(),
		"path":       sh.Path,
		"family":     string(sh.Family),
		"source":     sh.Source,
		"reason":     sh.Reason,
	}
}
