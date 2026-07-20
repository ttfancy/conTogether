package applog

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

const defaultQueueSize = 1024

// ErrClosed is returned by WriteLog once the Manager has been closed.
var ErrClosed = errors.New("applog: manager is closed")

// Manager is container-api's operational log store: asynchronous
// writes, level/time/substring-filtered reads, clearing, and a handler
// extension point (RegisterLogHandler) — everything container-api's
// REST/WebSocket log endpoints need, without any external logging
// dependency.
type Manager struct {
	queue chan *jsonEntry

	// stateMu guards `closed` and, critically, makes closing `queue`
	// race-free despite WriteLog being called from many goroutines:
	// WriteLog holds RLock for its entire check-then-send, and Close
	// takes Lock (which waits out every in-flight RLock holder) before
	// it closes the channel.
	stateMu sync.RWMutex
	closed  bool

	// mu guards entries and the backing file — both the write loop
	// (appending) and ReadLogs/ClearLogs (scanning/rewriting) touch
	// them.
	mu      sync.RWMutex
	entries []*jsonEntry
	file    *os.File // nil for an in-memory-only Manager (tests)
	path    string   // "" for in-memory-only

	handlersMu  sync.RWMutex
	handlers    []registeredHandler
	nextHandler int64

	wg sync.WaitGroup
}

type registeredHandler struct {
	id      int64
	handler LogHandler
}

// NewManager opens (creating if needed) a JSON-lines log file at path,
// loads any entries already in it, and starts the background write
// loop.
func NewManager(path string) (*Manager, error) {
	m := &Manager{queue: make(chan *jsonEntry, defaultQueueSize), path: path}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := loadEntries(f, &m.entries); err != nil {
		f.Close()
		return nil, err
	}
	m.file = f

	m.wg.Add(1)
	go m.run()
	return m, nil
}

// NewMemoryManager returns a Manager with no backing file — entries
// live only in the process, lost on exit. Used by tests, and anywhere
// else persistence across restarts doesn't matter.
func NewMemoryManager() *Manager {
	m := &Manager{queue: make(chan *jsonEntry, defaultQueueSize)}
	m.wg.Add(1)
	go m.run()
	return m
}

func loadEntries(f *os.File, out *[]*jsonEntry) error {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e jsonEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip a corrupt line rather than fail startup over it
		}
		*out = append(*out, &e)
	}
	return scanner.Err()
}

// WriteLog builds a LogEntry and enqueues it for asynchronous writing;
// it returns before the entry reaches storage or any handler.
func (m *Manager) WriteLog(level string, message string, fields ...Field) error {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	if m.closed {
		return ErrClosed
	}
	m.queue <- newEntry(parseLevel(level), message, fields)
	return nil
}

func (m *Manager) run() {
	defer m.wg.Done()
	for e := range m.queue {
		m.mu.Lock()
		m.entries = append(m.entries, e)
		if m.file != nil {
			if data, err := json.Marshal(e); err == nil {
				m.file.Write(append(data, '\n'))
			}
		}
		m.mu.Unlock()

		m.handlersMu.RLock()
		for _, rh := range m.handlers {
			rh.handler.Handle(e)
		}
		m.handlersMu.RUnlock()
	}
}

// ReadLogs returns stored entries at or above level that also satisfy
// filter, oldest first (the order they were written).
func (m *Manager) ReadLogs(level string, filter LogFilter) ([]LogEntry, error) {
	min := parseLevel(level)
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []LogEntry
	for _, e := range m.entries {
		if levelAtLeast(e.Lvl, min) && filter.matches(e) {
			out = append(out, e)
		}
	}
	return out, nil
}

// ClearLogs removes stored entries timestamped strictly before the
// given time, rewriting the backing file (if any) to match.
func (m *Manager) ClearLogs(before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	kept := m.entries[:0:0]
	for _, e := range m.entries {
		if !e.Ts.Before(before) {
			kept = append(kept, e)
		}
	}
	m.entries = kept

	if m.file == nil {
		return nil
	}
	if err := m.file.Truncate(0); err != nil {
		return err
	}
	if _, err := m.file.Seek(0, 0); err != nil {
		return err
	}
	for _, e := range kept {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := m.file.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// RegisterLogHandler adds a handler invoked for every entry as it is
// written — the extension point the WebSocket live tail
// (internal/wsstream) is built on. It returns an unregister function
// that removes the handler; callers tied to a connection's lifetime
// (like a WebSocket client) must call it on disconnect, or the handler
// leaks for the life of the Manager.
func (m *Manager) RegisterLogHandler(handler LogHandler) (unregister func()) {
	m.handlersMu.Lock()
	id := m.nextHandler
	m.nextHandler++
	m.handlers = append(m.handlers, registeredHandler{id: id, handler: handler})
	m.handlersMu.Unlock()

	return func() {
		m.handlersMu.Lock()
		defer m.handlersMu.Unlock()
		for i, rh := range m.handlers {
			if rh.id == id {
				m.handlers = append(m.handlers[:i], m.handlers[i+1:]...)
				return
			}
		}
	}
}

// Close stops accepting new entries, waits for the queue to drain, and
// closes the backing file (if any). Safe to call exactly once, as the
// last step of graceful shutdown.
func (m *Manager) Close() error {
	m.stateMu.Lock()
	m.closed = true
	close(m.queue) // safe: Lock() waited out every WriteLog holding RLock
	m.stateMu.Unlock()

	m.wg.Wait()

	if m.file != nil {
		return m.file.Close()
	}
	return nil
}
