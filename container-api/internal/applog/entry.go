// Package applog is container-api's own small, self-contained
// operational logger — request lifecycle, container events, job
// outcomes. It exists so container-api has no dependency on any
// external logging module.
//
// The shape here (async write, level filtering, a handler extension
// point) deliberately mirrors a well-understood logging design, but the
// storage is intentionally single-strategy (in-memory + an optional
// append-only JSON-lines file) rather than pluggable — container-api
// only ever needs the one backend, so the extra interface layer would
// be abstraction without a second implementation to justify it.
package applog

import (
	"strings"
	"time"
)

// Level identifies the severity of an Entry. Levels are ordered
// DebugLevel < InfoLevel < WarnLevel < ErrorLevel; ReadLogs treats the
// requested level as a minimum, not an exact match.
type Level string

const (
	DebugLevel Level = "DEBUG"
	InfoLevel  Level = "INFO"
	WarnLevel  Level = "WARN"
	ErrorLevel Level = "ERROR"
)

var levelRank = map[Level]int{
	DebugLevel: 0,
	InfoLevel:  1,
	WarnLevel:  2,
	ErrorLevel: 3,
}

// parseLevel normalizes a caller-supplied level string, defaulting to
// InfoLevel for anything unrecognized rather than rejecting the write.
func parseLevel(s string) Level {
	l := Level(strings.ToUpper(strings.TrimSpace(s)))
	if _, ok := levelRank[l]; !ok {
		return InfoLevel
	}
	return l
}

// levelAtLeast reports whether l is at least as severe as min.
func levelAtLeast(l, min Level) bool {
	return levelRank[l] >= levelRank[min]
}

// Field is a structured key/value pair attached to a log entry.
type Field struct {
	Key   string
	Value any
}

// F builds a Field; an ergonomic helper for WriteLog call sites, e.g.
// logger.WriteLog("INFO", "listening", applog.F("port", 8080)).
func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}

// LogEntry is a single log record. It's an interface (not a plain
// struct) so call sites that only ever read it (handlers, the HTTP
// layer) don't need to know it's backed by a JSON-loadable concrete
// type.
type LogEntry interface {
	Timestamp() time.Time
	Level() Level
	Message() string
	Fields() map[string]any
}

// jsonEntry is both the in-memory LogEntry implementation and the exact
// on-disk JSON-lines shape — one struct serves both, since there's only
// ever one storage strategy here.
type jsonEntry struct {
	Ts      time.Time      `json:"timestamp"`
	Lvl     Level          `json:"level"`
	Msg     string         `json:"message"`
	FieldsM map[string]any `json:"fields,omitempty"`
}

func (e *jsonEntry) Timestamp() time.Time   { return e.Ts }
func (e *jsonEntry) Level() Level           { return e.Lvl }
func (e *jsonEntry) Message() string        { return e.Msg }
func (e *jsonEntry) Fields() map[string]any { return e.FieldsM }

func newEntry(level Level, message string, fields []Field) *jsonEntry {
	var fieldMap map[string]any
	if len(fields) > 0 {
		fieldMap = make(map[string]any, len(fields))
		for _, f := range fields {
			fieldMap[f.Key] = f.Value
		}
	}
	return &jsonEntry{Ts: time.Now(), Lvl: level, Msg: message, FieldsM: fieldMap}
}
