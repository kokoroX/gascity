package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/sessionlog"
)

// agentListSessionReadCounter counts the session-store reads a request makes.
// censusCalls counts the by-type leg of a session.ListAll pass, so one census
// is one session-snapshot read; getCalls counts by-id reads.
type agentListSessionReadCounter struct {
	beads.Store
	censusCalls int
	getCalls    int
}

func (s *agentListSessionReadCounter) List(query beads.ListQuery) ([]beads.Bead, error) {
	if query.Type == session.BeadType {
		s.censusCalls++
	}
	return s.Store.List(query)
}

func (s *agentListSessionReadCounter) Get(id string) (beads.Bead, error) {
	s.getCalls++
	return s.Store.Get(id)
}

// getAgentListFromHandler serves GET /agents through h and decodes the list.
func getAgentListFromHandler(t *testing.T, h http.Handler, st *fakeState) agentListWire {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", cityURL(st, "/agents"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agents status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body agentListWire
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode GET /agents: %v", err)
	}
	return body
}

// claudeAgentListState builds a city of claude agents that are all running,
// all session-file attributable, and each recorded on a session bead under its
// canonical session name. It returns the state, the counting session store,
// and the agents' qualified names.
func claudeAgentListState(t *testing.T, count int) (*fakeState, *agentListSessionReadCounter, []string) {
	t.Helper()
	st := newFakeState(t)
	st.cfg.Workspace.Provider = "claude"
	st.cfg.Agents = nil
	st.cfg.NamedSessions = nil
	st.cfg.Rigs = []config.Rig{{Name: "myrig", Path: t.TempDir()}}
	st.cityBeadStore = beads.NewMemStore()
	st.stores = map[string]beads.Store{}
	sessions := beads.NewMemStore()

	workDirRoot := t.TempDir()
	var names []string
	for i := 1; i <= count; i++ {
		qualifiedName := fmt.Sprintf("myrig/agent-%d", i)
		names = append(names, qualifiedName)
		st.cfg.Agents = append(st.cfg.Agents, config.Agent{
			Name:              fmt.Sprintf("agent-%d", i),
			Dir:               "myrig",
			Provider:          "claude",
			WorkDir:           filepath.Join(workDirRoot, fmt.Sprintf("agent-%d", i)),
			MaxActiveSessions: intPtr(1),
		})
		sessionName := agentSessionName(st.cityName, qualifiedName, "")
		bead, err := sessions.Create(beads.Bead{
			Type:   session.BeadType,
			Labels: []string{session.LabelSession},
			Title:  qualifiedName,
			Metadata: map[string]string{
				"state":        string(session.StateActive),
				"template":     qualifiedName,
				"agent_name":   qualifiedName,
				"session_name": sessionName,
			},
		})
		if err != nil {
			t.Fatalf("create session bead for %s: %v", qualifiedName, err)
		}
		if err := st.sp.Start(context.Background(), sessionName, runtime.Config{}); err != nil {
			t.Fatalf("start runtime session %s: %v", sessionName, err)
		}
		if err := st.sp.SetMeta(sessionName, "GC_SESSION_ID", bead.ID); err != nil {
			t.Fatalf("set GC_SESSION_ID on %s: %v", sessionName, err)
		}
	}

	counter := &agentListSessionReadCounter{Store: sessions}
	st.sessionsBeadStore = counter
	return st, counter, names
}

// TestAgentListReadsSessionStoreOnce pins the cost of GET /agents: one session
// snapshot per request however many claude agents need model/context metadata,
// and no per-agent session-store Get.
func TestAgentListReadsSessionStoreOnce(t *testing.T) {
	for _, count := range []int{3, 6} {
		t.Run(fmt.Sprintf("%d agents", count), func(t *testing.T) {
			st, counter, names := claudeAgentListState(t, count)
			body := getAgentList(t, st, "")

			if len(body.Items) != count {
				t.Fatalf("GET /agents returned %d rows, want %d", len(body.Items), count)
			}
			for _, name := range names {
				row, ok := agentByNameIn(body.Items, name)
				if !ok {
					t.Fatalf("no agent row for %s; rows = %v", name, agentNames(body.Items))
				}
				if !row.Running {
					t.Fatalf("%s running = false, want true", name)
				}
			}

			if counter.getCalls != 0 {
				t.Errorf("session store Get calls = %d, want 0", counter.getCalls)
			}
			if counter.censusCalls != 1 {
				t.Errorf("session snapshot reads = %d, want 1", counter.censusCalls)
			}
		})
	}
}

// TestAgentListEnrichFallsBackToSessionID pins the second lookup rung: when no
// snapshot row names the running session, the session ID the row already read
// (GC_SESSION_ID) finds it, and its session key still discovers the keyed
// transcript.
func TestAgentListEnrichFallsBackToSessionID(t *testing.T) {
	st := newFakeState(t)
	st.cfg.Workspace.Provider = "claude"
	st.cfg.NamedSessions = nil
	st.cfg.Agents = []config.Agent{
		{Name: "worker", Dir: "myrig", Provider: "claude", MaxActiveSessions: intPtr(1)},
	}
	st.cfg.Rigs = []config.Rig{{Name: "myrig", Path: "/tmp/myrig"}}
	st.cityBeadStore = beads.NewMemStore()
	st.stores = map[string]beads.Store{}
	sessions := beads.NewMemStore()
	st.sessionsBeadStore = sessions

	canonical := agentSessionName(st.cityName, "myrig/worker", "")
	if err := st.sp.Start(context.Background(), canonical, runtime.Config{}); err != nil {
		t.Fatalf("start runtime session %s: %v", canonical, err)
	}
	const sessionKey = "11111111-2222-3333-4444-555555555555"
	bead, err := sessions.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession},
		Title:  "myrig/worker",
		Metadata: map[string]string{
			"state":        string(session.StateActive),
			"template":     "myrig/worker",
			"agent_name":   "myrig/worker",
			"session_name": "myrig--worker-recorded-elsewhere",
			"session_key":  sessionKey,
		},
	})
	if err != nil {
		t.Fatalf("create session bead: %v", err)
	}
	if err := st.sp.SetMeta(canonical, "GC_SESSION_ID", bead.ID); err != nil {
		t.Fatalf("set GC_SESSION_ID: %v", err)
	}

	searchDir := t.TempDir()
	slugDir := filepath.Join(searchDir, sessionlog.ProjectSlug("/tmp/myrig"))
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keyedPath := filepath.Join(slugDir, sessionKey+".jsonl")
	keyed := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-5-20251101","usage":{"input_tokens":10000,"cache_read_input_tokens":5000,"cache_creation_input_tokens":2000}}}` + "\n"
	if err := os.WriteFile(keyedPath, []byte(keyed), 0o644); err != nil {
		t.Fatal(err)
	}
	decoyPath := filepath.Join(slugDir, "newest-decoy.jsonl")
	decoy := `{"type":"assistant","message":{"role":"assistant","model":"claude-haiku-4-5-20251001","usage":{"input_tokens":10000}}}` + "\n"
	if err := os.WriteFile(decoyPath, []byte(decoy), 0o644); err != nil {
		t.Fatal(err)
	}
	// A workdir fallback would pick the newest file; only the session key
	// finds the keyed transcript.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(keyedPath, old, old); err != nil {
		t.Fatal(err)
	}

	srv := New(st)
	srv.sessionLogSearchPaths = []string{searchDir}
	h := newTestCityHandlerWith(t, st, srv)
	row := requireAgentRow(t, getAgentListFromHandler(t, h, st).Items, "myrig/worker")
	if row.Model != "claude-opus-4-5-20251101" {
		t.Errorf("Model = %q, want %q: the session ID lookup must supply the session key", row.Model, "claude-opus-4-5-20251101")
	}
	if row.ContextPct == nil {
		t.Error("expected non-nil ContextPct")
	}
}
