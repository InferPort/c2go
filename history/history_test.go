package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddEntryAndGetLastIP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	m := NewManager(path)

	if err := m.AddEntry("1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if ip := m.GetLastIP(); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}

	if err := m.AddEntry("5.6.7.8"); err != nil {
		t.Fatal(err)
	}
	if ip := m.GetLastIP(); ip != "5.6.7.8" {
		t.Errorf("expected 5.6.7.8, got %s", ip)
	}
}

func TestAddEntry_AppendOnlyFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	m := NewManager(path)

	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		if err := m.AddEntry(ip); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	for _, line := range lines {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Errorf("line is not a JSON object: %s", line)
		}
	}
}

func TestGetEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	m := NewManager(path)

	entries, err := m.GetEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty, got %d", len(entries))
	}

	m.AddEntry("1.2.3.4")
	entries, _ = m.GetEntries()
	if len(entries) != 1 || entries[0].IP != "1.2.3.4" {
		t.Errorf("unexpected entries: %+v", entries)
	}
}

func TestTrimExceedingMax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	m := NewManager(path)

	for i := range 60 {
		if err := m.AddEntry(ipFor(i)); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := m.GetEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 50 {
		t.Errorf("expected 50 entries after trim, got %d", len(entries))
	}
	if entries[0].IP != ipFor(10) {
		t.Errorf("expected first entry to be %s, got %s", ipFor(10), entries[0].IP)
	}
}

func TestBackwardCompatOldJSONArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	oldData := `[{"timestamp":"2024-01-01T00:00:00Z","ip":"9.9.9.9"},{"timestamp":"2024-01-02T00:00:00Z","ip":"8.8.8.8"}]`
	if err := os.WriteFile(path, []byte(oldData), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(path)
	ip := m.GetLastIP()
	if ip != "8.8.8.8" {
		t.Errorf("expected 8.8.8.8 from old format, got %s", ip)
	}

	if err := m.AddEntry("7.7.7.7"); err != nil {
		t.Fatal(err)
	}

	entries, err := m.GetEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries after migration, got %d", len(entries))
	}
	if entries[2].IP != "7.7.7.7" {
		t.Errorf("expected last entry 7.7.7.7, got %s", entries[2].IP)
	}
}

func TestGetLastIP_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope.json")
	m := NewManager(path)

	if ip := m.GetLastIP(); ip != "" {
		t.Errorf("expected empty, got %s", ip)
	}
}

func ipFor(n int) string {
	return fmt.Sprintf("10.0.0.%d", n)
}
