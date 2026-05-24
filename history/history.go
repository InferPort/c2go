package history

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// NewManager creates a new History manager that stores data at the given file path.
func NewManager(filePath string) *Manager {
	return &Manager{
		filePath: filePath,
	}
}

// AddEntry records a new IP change event, keeping only the last maxHistoryEntries.
func (m *Manager) AddEntry(ip string) error {
	entries, err := m.GetEntries()
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	newEntry := Entry{
		Timestamp: time.Now(),
		IP:        ip,
	}

	entries = append(entries, newEntry)

	// Keep only the last maxHistoryEntries
	if len(entries) > maxHistoryEntries {
		entries = entries[len(entries)-maxHistoryEntries:]
	}

	return m.saveEntries(entries)
}

// GetEntries returns all recorded IP change events.
func (m *Manager) GetEntries() ([]Entry, error) {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return nil, err
	}

	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}

	return entries, nil
}

// GetLastIP returns the most recent IP recorded, or an empty string if none.
func (m *Manager) GetLastIP() string {
	entries, err := m.GetEntries()
	if err != nil || len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].IP
}

func (m *Manager) saveEntries(entries []Entry) error {
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.filePath, data, 0644)
}
