// Copyright (c) 2025 JoeGlenn1213
// ActionD Artifact Store - Persists plugin outputs to disk

package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Store manages artifact storage for action outputs
type Store struct {
	baseDir string
}

// NewStore creates a new artifact store
func NewStore(baseDir string) *Store {
	if baseDir == "" {
		home, _ := os.UserHomeDir()
		baseDir = filepath.Join(home, ".localgithub", "actions")
	}
	return &Store{baseDir: baseDir}
}

// Root returns the base directory of the store
func (s *Store) Root() string {
	return s.baseDir
}

// Writer provides an interface for plugins to write artifacts
type Writer struct {
	dir string
}

// NewWriter creates a writer for a specific action execution
// Format: {baseDir}/{timestamp}_{eventType}/
func (s *Store) NewWriter(eventType, repo string) (*Writer, error) {
	// Create directory with timestamp and event type
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	dirName := fmt.Sprintf("%s_%s_%s", timestamp, eventType, sanitize(repo))
	return s.createWriter(dirName)
}

// NewWriterWithID creates a writer using the Job ID for isolation
// Format: {baseDir}/{timestamp}_{eventType}_{jobID}/
func (s *Store) NewWriterWithID(id, eventType, repo string) (*Writer, error) {
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	dirName := fmt.Sprintf("%s_%s_%s_%s", timestamp, eventType, sanitize(repo), id)
	return s.createWriter(dirName)
}

func (s *Store) createWriter(dirName string) (*Writer, error) {
	dir := filepath.Join(s.baseDir, dirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create artifact dir: %w", err)
	}
	return &Writer{dir: dir}, nil
}

// Write writes raw bytes to a file
func (w *Writer) Write(name string, data []byte) error {
	path := filepath.Join(w.dir, name)
	return os.WriteFile(path, data, 0644)
}

// WriteJSON writes a JSON-encoded value to a file
func (w *Writer) WriteJSON(name string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return w.Write(name, data)
}

// WriteString writes a string to a file
func (w *Writer) WriteString(name string, content string) error {
	return w.Write(name, []byte(content))
}

// Dir returns the artifact directory path
func (w *Writer) Dir() string {
	return w.dir
}

// sanitize replaces unsafe characters for directory names
func sanitize(s string) string {
	result := make([]byte, len(s))
	for i, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result[i] = c
		} else {
			result[i] = '_'
		}
	}
	return string(result)
}
