// Copyright (c) 2025 JoeGlenn1213
// ActionD Log Layers
// Provides structured, layered logging for different audiences

package log

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Layer represents a log layer/audience
type Layer string

const (
	LayerEvent    Layer = "event"    // Event log - what happened (git push, tag, etc.)
	LayerDispatch Layer = "dispatch" // Dispatch log - plugin matching, scheduling
	LayerPlugin   Layer = "plugin"   // Plugin log - execution output
	LayerUser     Layer = "user"     // User summary - human-readable summary
	LayerAI       Layer = "ai"       // AI summary - structured for AI consumption
)

// Level represents log severity
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Entry represents a log entry
type Entry struct {
	Timestamp time.Time              `json:"timestamp"`
	Layer     Layer                  `json:"layer"`
	Level     Level                  `json:"level"`
	Message   string                 `json:"message"`
	JobID     string                 `json:"job_id,omitempty"`
	Repo      string                 `json:"repo,omitempty"`
	Plugin    string                 `json:"plugin,omitempty"`
	Event     string                 `json:"event,omitempty"`
	Duration  int64                  `json:"duration_ms,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// Logger provides layered logging
type Logger struct {
	mu       sync.Mutex
	writers  map[Layer][]io.Writer
	jobID    string
	baseDir  string
	minLevel Level
}

// NewLogger creates a new layered logger
func NewLogger(baseDir string) *Logger {
	return &Logger{
		writers:  make(map[Layer][]io.Writer),
		baseDir:  baseDir,
		minLevel: LevelDebug,
	}
}

// SetJobID sets the current job ID for context
func (l *Logger) SetJobID(jobID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobID = jobID
}

// SetMinLevel sets the minimum log level
func (l *Logger) SetMinLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.minLevel = level
}

// AddWriter adds a writer for a specific layer
func (l *Logger) AddWriter(layer Layer, w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writers[layer] = append(l.writers[layer], w)
}

// AddFile adds a file writer for a specific layer
func (l *Logger) AddFile(layer Layer, filename string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Ensure directory exists
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	l.writers[layer] = append(l.writers[layer], f)
	return nil
}

// Log writes a log entry to the specified layer
func (l *Logger) Log(layer Layer, level Level, message string, data ...interface{}) {
	if !l.shouldLog(level) {
		return
	}

	entry := Entry{
		Timestamp: time.Now(),
		Layer:     layer,
		Level:     level,
		Message:   fmt.Sprintf(message, data...),
	}

	l.mu.Lock()
	jobID := l.jobID
	writers := l.writers[layer]
	l.mu.Unlock()

	entry.JobID = jobID
	l.writeEntry(layer, entry, writers)
}

// LogWithFields writes a log entry with additional fields
func (l *Logger) LogWithFields(layer Layer, level Level, message string, fields map[string]interface{}) {
	if !l.shouldLog(level) {
		return
	}

	entry := Entry{
		Timestamp: time.Now(),
		Layer:     layer,
		Level:     level,
		Message:   message,
		Data:      fields,
	}

	l.mu.Lock()
	jobID := l.jobID
	writers := l.writers[layer]
	l.mu.Unlock()

	entry.JobID = jobID
	l.writeEntry(layer, entry, writers)
}

// Event logs an event (git push, tag, etc.)
func (l *Logger) Event(eventType, repo, message string) {
	l.LogWithFields(LayerEvent, LevelInfo, message, map[string]interface{}{
		"event_type": eventType,
		"repo":       repo,
	})
}

// Dispatch logs a dispatch action (plugin matching)
func (l *Logger) Dispatch(plugin, repo, action string) {
	l.LogWithFields(LayerDispatch, LevelInfo, fmt.Sprintf("Dispatching %s for %s", plugin, repo), map[string]interface{}{
		"plugin": plugin,
		"repo":   repo,
		"action": action,
	})
}

// PluginOutput logs plugin execution output
func (l *Logger) PluginOutput(jobID, plugin, output string, isError bool) {
	level := LevelInfo
	if isError {
		level = LevelError
	}
	l.LogWithFields(LayerPlugin, level, output, map[string]interface{}{
		"plugin": plugin,
	})
}

// PluginComplete logs plugin completion
func (l *Logger) PluginComplete(jobID, plugin string, success bool, durationMs int64, err error) {
	level := LevelInfo
	message := fmt.Sprintf("Plugin %s completed in %dms", plugin, durationMs)
	if !success {
		level = LevelError
		message = fmt.Sprintf("Plugin %s failed after %dms", plugin, durationMs)
	}

	fields := map[string]interface{}{
		"plugin":      plugin,
		"success":     success,
		"duration_ms": durationMs,
	}

	if err != nil {
		fields["error"] = err.Error()
	}

	l.LogWithFields(LayerPlugin, level, message, fields)
}

// UserSummary logs a human-readable summary
func (l *Logger) UserSummary(jobID, repo, plugin string, success bool, summary string) {
	level := LevelInfo
	if !success {
		level = LevelWarn
	}
	l.LogWithFields(LayerUser, level, summary, map[string]interface{}{
		"repo":    repo,
		"plugin":  plugin,
		"success": success,
	})
}

// AISummary logs a structured AI summary
func (l *Logger) AISummary(jobID, repo, plugin string, result *AIResult) {
	data := map[string]interface{}{
		"repo":    repo,
		"plugin":  plugin,
		"status":  result.Status,
		"summary": result.Summary,
	}

	if result.Error != "" {
		data["error"] = result.Error
	}
	if len(result.Hints) > 0 {
		data["hints"] = result.Hints
	}
	if len(result.Artifacts) > 0 {
		data["artifacts"] = result.Artifacts
	}

	l.LogWithFields(LayerAI, LevelInfo, result.Summary, data)
}

// AIResult represents a structured result for AI consumption
type AIResult struct {
	Status    string   `json:"status"`              // success, failure, error
	Summary   string   `json:"summary"`             // 1-2 sentence summary
	Error     string   `json:"error,omitempty"`     // Error message if failed
	Hints     []string `json:"hints,omitempty"`     // Suggested actions
	Artifacts []string `json:"artifacts,omitempty"` // Produced files
}

func (l *Logger) shouldLog(level Level) bool {
	levels := map[Level]int{
		LevelDebug: 0,
		LevelInfo:  1,
		LevelWarn:  2,
		LevelError: 3,
	}
	return levels[level] >= levels[l.minLevel]
}

func (l *Logger) writeEntry(layer Layer, entry Entry, writers []io.Writer) {
	// Format as JSON
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal log entry: %v\n", err)
		return
	}

	// Write to all writers for this layer
	for _, w := range writers {
		if _, err := w.Write(append(data, '\n')); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write log: %v\n", err)
		}
	}

	// Also write to stdout for immediate visibility (if not AI layer)
	if layer != LayerAI {
		l.printFormatted(entry)
	}
}

func (l *Logger) printFormatted(entry Entry) {
	timestamp := entry.Timestamp.Format("15:04:05")
	var prefix string
	switch entry.Level {
	case LevelDebug:
		prefix = "🔍"
	case LevelInfo:
		prefix = "ℹ️"
	case LevelWarn:
		prefix = "⚠️"
	case LevelError:
		prefix = "❌"
	}

	var jobPrefix string
	if entry.JobID != "" {
		jobPrefix = fmt.Sprintf("[%s] ", entry.JobID[:8])
	}

	fmt.Printf("%s %s%s %s\n", timestamp, jobPrefix, prefix, entry.Message)
}

// ParseLevel parses a log level string
func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}
