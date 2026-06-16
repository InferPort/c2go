package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxHistoryEntries = 50

type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	IP        string    `json:"ip"`
}

type Manager struct {
	filePath string
}

func NewManager(filePath string) *Manager {
	return &Manager{filePath: filePath}
}

func (m *Manager) AddEntry(ip string) error {
	entry := Entry{
		Timestamp: time.Now(),
		IP:        ip,
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if err := m.migrateIfOldFormat(); err != nil {
		return err
	}

	f, err := os.OpenFile(m.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, string(line)); err != nil {
		f.Close()
		return err
	}
	f.Close()

	return m.trimIfNeeded()
}

func (m *Manager) migrateIfOldFormat() error {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return nil
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return nil
	}

	var oldEntries []Entry
	if err := json.Unmarshal([]byte(trimmed), &oldEntries); err != nil || len(oldEntries) == 0 {
		return nil
	}

	lines := make([]string, len(oldEntries))
	for i, e := range oldEntries {
		b, _ := json.Marshal(e)
		lines[i] = string(b)
	}

	return os.WriteFile(m.filePath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func (m *Manager) trimIfNeeded() error {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) <= maxHistoryEntries {
		return nil
	}

	lines = lines[len(lines)-maxHistoryEntries:]
	return os.WriteFile(m.filePath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func (m *Manager) readAllLines() ([]Entry, error) {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var oldEntries []Entry
		if err := json.Unmarshal([]byte(trimmed), &oldEntries); err == nil {
			return oldEntries, nil
		}
		return nil, nil
	}

	var entries []Entry
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			entries = append(entries, e)
		}
	}

	return entries, nil
}

func (m *Manager) GetEntries() ([]Entry, error) {
	return m.readAllLines()
}

func (m *Manager) GetLastIP() string {
	entries, err := m.GetEntries()
	if err != nil || len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].IP
}
