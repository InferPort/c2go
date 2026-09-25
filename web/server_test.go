package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNewServerDefaults(t *testing.T) {
	os.Unsetenv("CONFIG_HOST")
	os.Unsetenv("CONFIG_PORT")

	s := NewServer("", 0)
	if s.Host != "127.0.0.1" {
		t.Errorf("expected default host 127.0.0.1, got %s", s.Host)
	}
	if s.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", s.Port)
	}
}

func TestNewServerEnvOverrides(t *testing.T) {
	os.Setenv("CONFIG_HOST", "192.168.1.100")
	os.Setenv("CONFIG_PORT", "9090")
	defer func() {
		os.Unsetenv("CONFIG_HOST")
		os.Unsetenv("CONFIG_PORT")
	}()

	s := NewServer("", 0)
	if s.Host != "192.168.1.100" {
		t.Errorf("expected host 192.168.1.100, got %s", s.Host)
	}
	if s.Port != 9090 {
		t.Errorf("expected port 9090, got %d", s.Port)
	}
}

func TestInitialDataHandler(t *testing.T) {
	s := NewServer("127.0.0.1", 8080)
	req := httptest.NewRequest(http.MethodGet, "/api/initial-data", nil)
	rec := httptest.NewRecorder()

	s.handleInitialData(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestServerLifecycle(t *testing.T) {
	s := NewServer("127.0.0.1", 19280)
	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.Start(ctx)
	}()

	// Wait briefly then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			t.Errorf("unexpected error on server shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shutdown gracefully")
	}
}
