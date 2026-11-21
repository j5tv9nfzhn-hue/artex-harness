package server

import (
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
)

// Workspace 文件管理 HTTP 口已随 S6-TRIM 删除，只剩上传文件的两个底层
// 函数：saveUpload（server/chatupload.go 的旧版对话上传仍在用）与
// writeFileAtomic（原子写文件）。

func saveUpload(hdr *multipart.FileHeader, dest string) error {
	src, err := hdr.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	return writeFileAtomic(dest, src)
}

// writeFileAtomic writes r to dest via a temp file in the same directory followed
// by a rename.
//
// Upstream used os.Create, which truncates the destination immediately: a dropped
// connection mid-upload left a truncated half file and the original was already
// gone (data loss, not a UX problem — these files are scripts/dictionaries/
// evidence). Temp-then-rename makes the replacement atomic, so the destination is
// either the old content or the complete new content.
//
// Overwrite-on-same-name is intentionally preserved: the API contract is
// unchanged (callers get 200 and the file is replaced), only the crash window is
// closed. (上游复核 docs/upstream/SOURCE-REVIEW.md 第 5 条，本项目已修一半：
// 保留同名覆盖语义，消除半截文件。)
func writeFileAtomic(dest string, r io.Reader) (err error) {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".upload-*.part")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName) // never leave a .part behind on failure
		}
	}()
	if _, err = io.Copy(tmp, r); err != nil {
		return err
	}
	// Flush to disk before the rename so a crash cannot expose an empty file under
	// the final name (rename is atomic, but the page cache is not).
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// Keep the uploaded file's permissions predictable (CreateTemp uses 0600).
	if err = os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}
