package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
)

type sideRun struct {
	key    string
	parent sidequestion.Parent
	cancel context.CancelFunc
	done   chan struct{}
}
type sideQuestionState struct {
	mu           sync.Mutex
	commands     sync.Mutex // short admission / clear operations only, never inference
	pending      map[string]sidequestion.Snapshot
	latest       map[string]sidequestion.Snapshot
	seen         map[string][2]int64
	runs         map[string]sideRun
	done         chan struct{}
	outputTokens int
}

func sideModel(cfg agent.Config, id int64, name string) sidequestion.Model {
	b, _ := json.Marshal([]any{cfg.Format, cfg.BaseURL, cfg.Model, cfg.ThinkingType, cfg.ReasoningEffort, cfg.Stream, cfg.MaxTokensField, cfg.SessionHeaderKey})
	h := sha256.Sum256(b)
	return sidequestion.Model{ProfileID: id, Name: name, Format: string(cfg.Format), Model: cfg.Model, Identity: hex.EncodeToString(h[:]), Streaming: cfg.Stream, WindowTokens: cfg.CompactionWindow()}
}

func bindSideProvider(p llm.Provider, cfg agent.Config, id int64, name string) llm.Provider {
	return sidequestion.Bind(p, sideModel(cfg, id, name))
}

func (s *Server) initSideQuestions() {
	s.side = &sideQuestionState{pending: map[string]sidequestion.Snapshot{}, latest: map[string]sidequestion.Snapshot{}, seen: map[string][2]int64{}, runs: map[string]sideRun{}, done: make(chan struct{})}
	if n, err := strconv.Atoi(os.Getenv("ARTEX_BTW_MAX_OUTPUT_TOKENS")); err == nil && n >= 256 && n <= 32768 {
		s.side.outputTokens = n
	}
	if s.m.pg == nil {
		close(s.side.done)
		return
	}
	if err := s.m.pg.InterruptSideRequests(s.ctx); err != nil {
		log.Printf("[btw] recover: %v", err)
	}
	s.ctx = sidequestion.WithPublisher(s.ctx, func(snap sidequestion.Snapshot) {
		s.side.mu.Lock()
		defer s.side.mu.Unlock()
		key := snap.Parent.Key()
		old, ok := s.side.seen[key]
		if ok && (old[0] > snap.RunID || old[0] == snap.RunID && old[1] >= snap.Version) {
			return
		}
		s.side.latest[key] = snap
		s.side.seen[key] = [2]int64{snap.RunID, snap.Version}
		s.side.pending[key] = snap
	})
	go func() {
		defer close(s.side.done)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				s.flushSideSnapshots()
			case <-s.ctx.Done():
				s.flushSideSnapshots()
				return
			}
		}
	}()
}

func (s *Server) flushSideSnapshots() {
	if s.side == nil || s.m.pg == nil {
		return
	}
	s.side.commands.Lock()
	defer s.side.commands.Unlock()
	s.side.mu.Lock()
	pending := s.side.pending
	s.side.pending = map[string]sidequestion.Snapshot{}
	s.side.mu.Unlock()
	for key, snap := range pending {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := s.m.pg.SaveSideSnapshot(ctx, snap)
		cancel()
		s.side.mu.Lock()
		if err == nil || errors.Is(err, db.ErrSideParentGone) {
			if cur, ok := s.side.latest[key]; ok && cur.RunID == snap.RunID && cur.Version == snap.Version {
				delete(s.side.latest, key)
			}
		} else if newer, ok := s.side.pending[key]; !ok || newer.RunID < snap.RunID || newer.RunID == snap.RunID && newer.Version < snap.Version {
			s.side.pending[key] = snap
		}
		s.side.mu.Unlock()
		if err != nil && !errors.Is(err, db.ErrSideParentGone) {
			log.Printf("[btw] checkpoint persistence: %v", err)
		}
	}
}

func (s *Server) sideSnapshot(ctx context.Context, p sidequestion.Parent) (*sidequestion.Snapshot, error) {
	s.side.mu.Lock()
	snap, ok := s.side.latest[p.Key()]
	s.side.mu.Unlock()
	if ok {
		return &snap, nil
	}
	return s.m.pg.SideSnapshot(ctx, p.Key())
}

func (s *Server) cancelSideWhere(match func(sidequestion.Parent) bool) []<-chan struct{} {
	if s.side == nil {
		return nil
	}
	s.side.mu.Lock()
	defer s.side.mu.Unlock()
	var done []<-chan struct{}
	for _, run := range s.side.runs {
		if match(run.parent) {
			run.cancel()
			done = append(done, run.done)
		}
	}
	return done
}

// Called after the task admission barrier closes and the main loops drain.
// Wait for final answer/usage writes before taking the archive snapshot.
func (s *Server) drainTaskSideQuestions(ctx context.Context, taskID string) error {
	if s.side == nil {
		return nil
	}
	id, _ := strconv.ParseInt(taskID, 10, 64)
	s.side.commands.Lock()
	done := s.cancelSideWhere(func(p sidequestion.Parent) bool { return p.TaskID == id })
	s.side.commands.Unlock()
	for _, ch := range done {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.flushSideSnapshots()
	// A failed flush must not silently leave the latest checkpoint out of archive.
	s.side.mu.Lock()
	defer s.side.mu.Unlock()
	for _, snap := range s.side.pending {
		if snap.Parent.TaskID == id {
			return errors.New("旁路上下文尚未保存，请重试")
		}
	}
	return nil
}

func (s *Server) cancelWorkerSide(taskID string, expID, iid int64) {
	if s.side == nil {
		return
	}
	id, _ := strconv.ParseInt(taskID, 10, 64)
	p := sidequestion.Parent{TaskID: id, ExplorationID: expID, IntentID: iid}
	s.side.commands.Lock()
	defer s.side.commands.Unlock()
	s.cancelSideWhere(func(parent sidequestion.Parent) bool { return parent == p })
}

func (s *Server) sideProvider(model sidequestion.Model) (llm.Provider, error) {
	var p llm.Provider
	var cfg agent.Config
	var ok bool
	if model.ProfileID > 0 {
		// Validate the persisted reference even if a previous provider is cached.
		current, exists := s.loadProfileConfig(model.ProfileID)
		if !exists || sideModel(current, model.ProfileID, model.Name).Identity != model.Identity {
			return nil, errors.New("模型配置已删除或变化，请先运行主 Agent 更新上下文")
		}
		p, cfg, ok = s.providerForProfile(model.ProfileID)
	} else {
		s.cfgMu.Lock()
		p, cfg, ok = s.llmDirect, s.llmCfg, s.llmOn
		s.cfgMu.Unlock()
	}
	if !ok || p == nil || sideModel(cfg, model.ProfileID, model.Name).Identity != model.Identity {
		return nil, errors.New("模型配置已删除或变化，请先运行主 Agent 更新上下文")
	}
	return p, nil
}
