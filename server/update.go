package server

import (
	"sync"

	"github.com/Autumn-27/artex/selfupdate"
)

// 页面一键更新的 HTTP 面（check/apply/rollback/stream）已随 S6-TRIM 删除。
// 只剩启动时与 main 之间的两个约定：
//   - RestartRequested：main 在它关闭时以 selfupdate.ExitRestart 退出，
//     交给守护脚本重新拉起（现在没有调用方会关闭它，通道保持打开）；
//   - SetBootUpdateState：main 在启动时注入 selfupdate.Bootstrap 的结论。

// restartCh 在升级就绪或回滚完成后关闭，main 收到后以 ExitRestart 退出。
var restartCh = make(chan struct{})

// RestartRequested 返回一个在"请退出并让守护进程重新拉起我"时关闭的 channel。
func RestartRequested() <-chan struct{} { return restartCh }

// bootState 是本次启动时 selfupdate.Bootstrap 的结论（升级成功 / 刚回滚 /
// 暂存件被丢弃），由 main 注入。
var (
	bootStateMu sync.Mutex
	bootState   selfupdate.State
)

// SetBootUpdateState 由 main 在启动时调用一次。
func SetBootUpdateState(st selfupdate.State) {
	bootStateMu.Lock()
	defer bootStateMu.Unlock()
	bootState = st
}

func bootUpdateState() selfupdate.State {
	bootStateMu.Lock()
	defer bootStateMu.Unlock()
	return bootState
}
