package server

import (
	"github.com/Autumn-27/artex/db"
)

// 资产/公司的 HTTP 口已随 S6-TRIM 删除（管理面）。只剩 assetStore：
// agent 工具层（server/platform_tools.go）仍直接读 db 的资产表。

// assetStore returns the asset store.
func (s *Server) assetStore() *db.AssetStore {
	if s.m.pg == nil {
		return nil
	}
	return s.m.pg.Assets()
}
