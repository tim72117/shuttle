package api

// clientToolsSessions tracks currently-connected clientToolsSession
// instances so an HTTP request (handleClientToolsTestPrompt) can reach "the
// page" without itself being a WS message — this POC only ever expects one
// browser tab connected at a time (see task's own framing: prove the
// mechanism works, not build a multi-tab-aware directory), so "most
// recently connected, still open" is a deliberately simple enough answer;
// see current() below.
import "sync"

type clientToolsSessions struct {
	mu    sync.Mutex
	byID  map[string]*clientToolsSession
	order []string // connection order, most recent last
}

func newClientToolsSessions() *clientToolsSessions {
	return &clientToolsSessions{byID: make(map[string]*clientToolsSession)}
}

func (r *clientToolsSessions) add(s *clientToolsSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[s.id] = s
	r.order = append(r.order, s.id)
}

func (r *clientToolsSessions) remove(s *clientToolsSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, s.id)
	for i, id := range r.order {
		if id == s.id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// current returns the most recently connected still-open session, or
// ok=false if none is connected — the test-prompt endpoint's "which page do
// I send this to" answer for this POC's single-tab-at-a-time scope.
func (r *clientToolsSessions) current() (s *clientToolsSession, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.order) == 0 {
		return nil, false
	}
	id := r.order[len(r.order)-1]
	s, ok = r.byID[id]
	return s, ok
}
