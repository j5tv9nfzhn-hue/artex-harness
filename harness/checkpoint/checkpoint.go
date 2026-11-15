// Package checkpoint records per-turn file snapshots for a task workspace.
//
// The console's "undo this turn" needs a restore point, but the target machine is
// not guaranteed to have git (docs/07 P1). Existing code solves the same problem
// for DIFF by hashing the tree against a baseline; this package generalises it:
// each checkpoint is a manifest of relative path → sha256, and restore writes the
// recorded bytes back from a stored blob directory.
//
// Honest limitation, stated in the API: only regular files captured by the
// snapshot are restorable. Shell commands that wrote outside the workspace, or
// files larger than the capture cap, are not — the preview must say so rather
// than pretending a full rollback.
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// MaxFileBytes bounds one captured file. Large binaries/wordlists are skipped
	// instead of bloating the snapshot; the manifest marks them as skipped.
	MaxFileBytes = 1 << 20
	// SkipDirs are harness bookkeeping directories, never user content.
	SkipDirs     = ".harness-baseline"
	manifestName = "manifest.json"
	blobsDir     = "blobs"
)

// File is one entry in a checkpoint manifest.
type File struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
	Mode uint32 `json:"mode"`
	// Skipped is non-empty when the file exceeded the cap or could not be read.
	Skipped string `json:"skipped,omitempty"`
}

// Checkpoint is one restore point.
type Checkpoint struct {
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	Files     []File    `json:"files"`
	// Untracked lists live paths that the snapshot did not capture (so restore
	// can tell the operator they would remain).
	Untracked []string `json:"untracked,omitempty"`
}

// Store owns the on-disk snapshots for one task. Layout:
//
//	<root>/checkpoints/<revision>/manifest.json
//	<root>/checkpoints/blobs/<sha256>        (content-addressed, shared)
//
// Blobs are content-addressed so ten checkpoints that share a file store it once.
type Store struct {
	root string // task workspace root
	dir  string // checkpoint root (kept OUTSIDE the workspace when it is a project dir)
}

// New returns a Store. checkpointDir may be empty, in which case snapshots live
// under <workspace>/.harness-checkpoints (skipped by the workspace walker).
func New(workspace, checkpointDir string) *Store {
	if checkpointDir == "" {
		checkpointDir = filepath.Join(workspace, ".harness-checkpoints")
	}
	return &Store{root: workspace, dir: checkpointDir}
}

// Dir exposes the snapshot directory (for the API to report).
func (s *Store) Dir() string { return s.dir }

// Capture hashes the live workspace and stores a checkpoint for revision.
// The optional skip names are top-level directories that must not be captured.
func (s *Store) Capture(revision int64, at time.Time) (Checkpoint, error) {
	if revision <= 0 {
		return Checkpoint{}, errors.New("revision must be > 0")
	}
	if at.IsZero() {
		at = time.Now()
	}
	cp := Checkpoint{Revision: revision, CreatedAt: at, Files: []File{}}
	live := map[string]bool{}
	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if rel == SkipDirs || strings.HasPrefix(rel, SkipDirs+string(os.PathSeparator)) ||
			rel == ".harness-checkpoints" || strings.HasPrefix(rel, ".harness-checkpoints"+string(os.PathSeparator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		live[rel] = true
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			cp.Files = append(cp.Files, File{Path: rel, Skipped: "stat 失败"})
			return nil
		}
		if info.Size() > MaxFileBytes {
			cp.Files = append(cp.Files, File{Path: rel, Size: info.Size(), Skipped: "超过快照上限"})
			return nil
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			cp.Files = append(cp.Files, File{Path: rel, Skipped: "读取失败"})
			return nil
		}
		data, rerr := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		f.Close()
		if rerr != nil || len(data) > MaxFileBytes {
			cp.Files = append(cp.Files, File{Path: rel, Skipped: "读取失败/超限"})
			return nil
		}
		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:])
		if berr := s.writeBlob(hash, data); berr != nil {
			return berr
		}
		cp.Files = append(cp.Files, File{Path: rel, Hash: hash, Size: info.Size(), Mode: uint32(info.Mode().Perm())})
		return nil
	})
	if err != nil {
		return Checkpoint{}, err
	}
	for _, f := range cp.Files {
		delete(live, f.Path)
	}
	for rel := range live {
		cp.Untracked = append(cp.Untracked, rel)
	}
	sort.Slice(cp.Files, func(i, j int) bool { return cp.Files[i].Path < cp.Files[j].Path })
	sort.Strings(cp.Untracked)
	if err := s.save(cp); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

func (s *Store) writeBlob(hash string, data []byte) error {
	dir := filepath.Join(s.dir, blobsDir, hash[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, hash)
	if _, err := os.Stat(dst); err == nil {
		return nil // content-addressed: already stored
	}
	tmp, err := os.CreateTemp(dir, ".blob-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func (s *Store) save(cp Checkpoint) error {
	dir := filepath.Join(s.dir, "revs", fmt.Sprint(cp.Revision))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, manifestName))
}

// List returns all checkpoints in ascending revision order.
func (s *Store) List() ([]Checkpoint, error) {
	revs := filepath.Join(s.dir, "revs")
	entries, err := os.ReadDir(revs)
	if os.IsNotExist(err) {
		return []Checkpoint{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Checkpoint{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var rev int64
		if _, err := fmt.Sscanf(e.Name(), "%d", &rev); err != nil {
			continue
		}
		cp, err := s.Load(rev)
		if err == nil {
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out, nil
}

// Load reads one checkpoint's manifest.
func (s *Store) Load(revision int64) (Checkpoint, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "revs", fmt.Sprint(revision), manifestName))
	if err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint %d: %w", revision, err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// Plan describes what a restore would do, WITHOUT mutating the workspace.
type Plan struct {
	Revision   int64    `json:"revision"`
	Restorable []string `json:"restorable"` // files with a stored blob
	Skip       []string `json:"skip"`       // captured but not restorable
	Deleted    []string `json:"deleted"`    // live files absent from the checkpoint (would be removed)
	Added      []string `json:"added"`      // checkpoint files missing live (would be recreated)
	Untracked  []string `json:"untracked"`  // live paths never captured (would REMAIN)
	Note       string   `json:"note"`
}

// Preview computes the restore plan for a checkpoint.
func (s *Store) Preview(revision int64) (Plan, error) {
	cp, err := s.Load(revision)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Revision: revision, Restorable: []string{}, Skip: []string{}, Deleted: []string{}, Added: []string{}}
	live := map[string]bool{}
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if rel == SkipDirs || strings.HasPrefix(rel, SkipDirs+string(os.PathSeparator)) ||
			rel == ".harness-checkpoints" || strings.HasPrefix(rel, ".harness-checkpoints"+string(os.PathSeparator)) {
			return nil
		}
		live[rel] = true
		info, ierr := d.Info()
		if ierr == nil && info.Size() > MaxFileBytes {
			p.Untracked = append(p.Untracked, rel+"（超过快照上限，不会回滚）")
		}
		return nil
	})
	for _, u := range cp.Untracked {
		p.Untracked = append(p.Untracked, u)
	}
	sort.Strings(p.Untracked)
	hashes := hashLive(s.root)
	for _, f := range cp.Files {
		if f.Skipped != "" || f.Hash == "" {
			p.Skip = append(p.Skip, f.Path+"（"+f.Skipped+"）")
			continue
		}
		if _, err := os.Stat(s.blobPath(f.Hash)); err != nil {
			p.Skip = append(p.Skip, f.Path+"（快照正文缺失）")
			continue
		}
		p.Restorable = append(p.Restorable, f.Path)
		if _, ok := hashes[f.Path]; !ok {
			p.Added = append(p.Added, f.Path)
		}
		delete(hashes, f.Path)
		delete(live, f.Path)
	}
	for rel := range hashes {
		p.Deleted = append(p.Deleted, rel)
		delete(live, rel)
	}
	for rel := range live {
		if !containsName(p.Untracked, rel) {
			p.Untracked = append(p.Untracked, rel+"（未被快照捕获，不会回滚）")
		}
	}
	sort.Strings(p.Deleted)
	sort.Strings(p.Added)
	sort.Strings(p.Skip)
	sort.Strings(p.Untracked)
	p.Note = "只恢复快照捕获的常规文件；Shell 命令写到工作区之外的内容、超过 " +
		fmt.Sprint(MaxFileBytes) + " 字节的大文件、以及从未被捕获的路径（untracked）不会回滚。需要兜底请用 git。"
	return p, nil
}

func containsName(ss []string, want string) bool {
	for _, s := range ss {
		if s == want || strings.HasPrefix(s, want+"（") {
			return true
		}
	}
	return false
}

// Restore applies a checkpoint: removes live files absent from the snapshot and
// writes the recorded bytes back. It is deliberately explicit (no silent
// fallback): the caller must have shown Preview first.
func (s *Store) Restore(revision int64) (Plan, error) {
	plan, err := s.Preview(revision)
	if err != nil {
		return Plan{}, err
	}
	cp, err := s.Load(revision)
	if err != nil {
		return Plan{}, err
	}
	for _, rel := range plan.Deleted {
		if p := s.safePath(rel); p != "" {
			_ = os.Remove(p)
		}
	}
	for _, f := range cp.Files {
		if f.Skipped != "" || f.Hash == "" {
			continue
		}
		data, err := os.ReadFile(s.blobPath(f.Hash))
		if err != nil {
			continue
		}
		dst := s.safePath(f.Path)
		if dst == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return plan, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(dst), ".restore-*")
		if err != nil {
			return plan, err
		}
		name := tmp.Name()
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			os.Remove(name)
			return plan, err
		}
		if err := tmp.Chmod(os.FileMode(f.Mode)); err != nil {
			tmp.Close()
			os.Remove(name)
			return plan, err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(name)
			return plan, err
		}
		if err := os.Rename(name, dst); err != nil {
			os.Remove(name)
			return plan, err
		}
	}
	return plan, nil
}

func (s *Store) blobPath(hash string) string {
	return filepath.Join(s.dir, blobsDir, hash[:2], hash)
}

// safePath returns the joined path (empty string for an invalid/traversing rel).
// Callers must skip empty results; a tampered manifest must never write outside
// the workspace.
func (s *Store) safePath(rel string) string {
	if rel == "" || filepath.IsAbs(rel) {
		return ""
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return ""
		}
	}
	return filepath.Join(s.root, rel)
}

func hashLive(root string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil || rel == "." {
			return nil
		}
		if rel == SkipDirs || strings.HasPrefix(rel, SkipDirs+string(os.PathSeparator)) ||
			rel == ".harness-checkpoints" || strings.HasPrefix(rel, ".harness-checkpoints"+string(os.PathSeparator)) {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > MaxFileBytes {
			return nil
		}
		f, oerr := os.Open(p)
		if oerr != nil {
			return nil
		}
		defer f.Close()
		h := sha256.New()
		if _, cerr := io.Copy(h, f); cerr != nil {
			return nil
		}
		out[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return out
}
