package ipcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsValidIP_ValidIPv4(t *testing.T) {
	if !isValidIP("192.168.1.1") {
		t.Error("expected valid IPv4")
	}
}

func TestIsValidIP_ValidIPv6(t *testing.T) {
	if !isValidIP("::1") {
		t.Error("expected valid IPv6")
	}
	if !isValidIP("2001:db8::1") {
		t.Error("expected valid IPv6")
	}
}

func TestIsValidIP_Invalid(t *testing.T) {
	invalid := []string{"", "not-an-ip", "abc.def.ghi.jkl", "256.256.256.256", "<html>", "1.2.3", "..."}
	for _, ip := range invalid {
		if isValidIP(ip) {
			t.Errorf("expected invalid IP: %s", ip)
		}
	}
}

func TestGetPublicIP_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("203.0.113.42"))
	}))
	defer srv.Close()

	originalProviders := providers
	providers = []string{srv.URL}
	defer func() { providers = originalProviders }()

	ip, err := GetPublicIP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.42" {
		t.Errorf("expected 203.0.113.42, got %s", ip)
	}
}

func TestGetPublicIP_ConcurrentFallback(t *testing.T) {
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("198.51.100.1"))
	}))
	defer srv2.Close()

	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("should not matter"))
	}))
	defer srv3.Close()

	originalProviders := providers
	providers = []string{srv1.URL, srv2.URL, srv3.URL}
	defer func() { providers = originalProviders }()

	ip, err := GetPublicIP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "198.51.100.1" {
		t.Errorf("expected 198.51.100.1, got %s", ip)
	}
}

func TestGetPublicIP_AllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	originalProviders := providers
	providers = []string{srv.URL, srv.URL, srv.URL}
	defer func() { providers = originalProviders }()

	_, err := GetPublicIP(context.Background())
	if err != ErrNoInternet {
		t.Errorf("expected ErrNoInternet, got %v", err)
	}
}

func TestGetPublicIP_RejectsGarbage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>not an ip</html>"))
	}))
	defer srv.Close()

	originalProviders := providers
	providers = []string{srv.URL}
	defer func() { providers = originalProviders }()

	_, err := GetPublicIP(context.Background())
	if err == nil {
		t.Error("expected error for garbage response")
	}
}

func TestGetPublicIP_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("1.2.3.4"))
	}))
	defer srv.Close()

	originalProviders := providers
	providers = []string{srv.URL}
	defer func() { providers = originalProviders }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := GetPublicIP(ctx)
	if err == nil {
		t.Error("expected error due to context cancellation")
	}
}

func TestGetPublicIP_TraceFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fl=123\nh=cloudflare.com\nip=192.0.2.1\nts=123456\n"))
	}))
	defer srv.Close()

	originalProviders := providers
	providers = []string{srv.URL + "/cdn-cgi/trace"}
	defer func() { providers = originalProviders }()

	ip, err := GetPublicIP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "192.0.2.1" {
		t.Errorf("expected 192.0.2.1, got %s", ip)
	}
}
