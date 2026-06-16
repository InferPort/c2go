package ipcheck

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var (
	ErrNoInternet = errors.New("no internet connection or all IP services failed")
)

var providers = []string{
	"https://1.1.1.1/cdn-cgi/trace",
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

var httpClient = &http.Client{
	Timeout: 5 * time.Second,
}

func GetPublicIP(ctx context.Context) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		ip  string
		err error
	}
	results := make(chan result, len(providers))

	for _, url := range providers {
		url := url
		go func() {
			ip, err := fetchIP(ctx, url)
			select {
			case results <- result{ip, err}:
			case <-ctx.Done():
			}
		}()
	}

	for range providers {
		select {
		case res := <-results:
			if res.err == nil && isValidIP(res.ip) {
				cancel()
				return res.ip, nil
			}
		case <-ctx.Done():
			return "", ErrNoInternet
		}
	}

	return "", ErrNoInternet
}

func fetchIP(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("non-200 status code")
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	bodyStr := string(bodyBytes)
	if strings.Contains(url, "cdn-cgi/trace") {
		lines := strings.Split(bodyStr, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "ip=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "ip=")), nil
			}
		}
		return "", errors.New("ip key not found in trace response")
	}

	return strings.TrimSpace(bodyStr), nil
}

func isValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}
