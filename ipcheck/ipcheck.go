package ipcheck

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"c2go/console"
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
		provider string
		ip       string
		err      error
		duration time.Duration
	}
	results := make(chan result, len(providers))

	console.LogDebug("Querying %d IP providers concurrently...", len(providers))

	for _, url := range providers {
		url := url
		go func() {
			start := time.Now()
			console.LogDebug("  [→] Firing request to: %s", url)
			ip, err := fetchIP(ctx, url)
			elapsed := time.Since(start)
			select {
			case results <- result{provider: url, ip: ip, err: err, duration: elapsed}:
			case <-ctx.Done():
			}
		}()
	}

	for range providers {
		select {
		case res := <-results:
			if res.err == nil && isValidIP(res.ip) {
				console.LogDebug("  [★] Provider WON the race: %s -> %s (latency: %v)", res.provider, res.ip, res.duration.Round(time.Millisecond))
				console.LogDebug("  [⤓] Cancelling remaining provider requests")
				cancel()
				return res.ip, nil
			} else if res.err != nil {
				console.LogDebug("  [✕] Provider failed: %s (%v)", res.provider, res.err)
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

type InterfaceInfo struct {
	Name  string   `json:"name"`
	IPs   []string `json:"ips"`
	Flags []string `json:"flags"`
	IsUp  bool     `json:"is_up"`
	MAC   string   `json:"mac"`
}

// GetNetworkInterfaces lists all non-loopback network interfaces with their local IPs
func GetNetworkInterfaces() ([]InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var result []InterfaceInfo
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		var ips []string
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				ips = append(ips, ipNet.IP.String())
			}
		}

		var flagStrs []string
		if iface.Flags&net.FlagUp != 0 {
			flagStrs = append(flagStrs, "UP")
		}
		if iface.Flags&net.FlagBroadcast != 0 {
			flagStrs = append(flagStrs, "BROADCAST")
		}
		if iface.Flags&net.FlagPointToPoint != 0 {
			flagStrs = append(flagStrs, "P2P")
		}
		if iface.Flags&net.FlagMulticast != 0 {
			flagStrs = append(flagStrs, "MULTICAST")
		}

		result = append(result, InterfaceInfo{
			Name:  iface.Name,
			IPs:   ips,
			Flags: flagStrs,
			IsUp:  iface.Flags&net.FlagUp != 0 && len(ips) > 0,
			MAC:   iface.HardwareAddr.String(),
		})
	}

	return result, nil
}

// GetPublicIPWithInterfaces resolves public IP by checking preferred interfaces in priority order
func GetPublicIPWithInterfaces(ctx context.Context, preferredInterfaces []string) (string, error) {
	if len(preferredInterfaces) == 0 {
		return GetPublicIP(ctx)
	}

	console.LogDebug("Resolving IP with preferred interface priority: %v", preferredInterfaces)

	for i, ifaceName := range preferredInterfaces {
		console.LogDebug("  [%d/%d] Inspecting interface '%s'...", i+1, len(preferredInterfaces), ifaceName)
		iface, err := net.InterfaceByName(ifaceName)
		if err != nil || iface.Flags&net.FlagUp == 0 {
			console.LogDebug("  [!] Interface '%s' is DOWN or unavailable, trying next fallback", ifaceName)
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			console.LogDebug("  [!] Failed reading addresses for '%s': %v", ifaceName, err)
			continue
		}

		var localIP net.IP
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				localIP = ipNet.IP
				break
			}
		}

		if localIP == nil {
			console.LogDebug("  [!] Interface '%s' has no valid IP assigned, trying next fallback", ifaceName)
			continue
		}

		console.LogDebug("  [✓] Binding socket to interface '%s' (local IP: %s)", ifaceName, localIP)

		// Create interface-bound client
		boundTransport := &http.Transport{
			DialContext: (&net.Dialer{
				LocalAddr: &net.TCPAddr{IP: localIP},
				Timeout:   3 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 3 * time.Second,
		}
		boundClient := &http.Client{
			Transport: boundTransport,
			Timeout:   5 * time.Second,
		}

		// Try providers with bound client
		for _, url := range providers {
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				continue
			}
			resp, err := boundClient.Do(req)
			if err != nil {
				console.LogDebug("    [✕] (%s) Provider %s failed: %v", ifaceName, url, err)
				continue
			}
			bodyBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				console.LogDebug("    [✕] (%s) Provider %s returned status %d", ifaceName, url, resp.StatusCode)
				continue
			}

			bodyStr := strings.TrimSpace(string(bodyBytes))
			elapsed := time.Since(start).Round(time.Millisecond)
			if strings.Contains(url, "cdn-cgi/trace") {
				for _, line := range strings.Split(bodyStr, "\n") {
					if strings.HasPrefix(line, "ip=") {
						ip := strings.TrimSpace(strings.TrimPrefix(line, "ip="))
						if isValidIP(ip) {
							console.LogDebug("    [★] (%s) Provider %s resolved IP: %s (%v)", ifaceName, url, ip, elapsed)
							return ip, nil
						}
					}
				}
				continue
			}

			if isValidIP(bodyStr) {
				console.LogDebug("    [★] (%s) Provider %s resolved IP: %s (%v)", ifaceName, url, bodyStr, elapsed)
				return bodyStr, nil
			}
		}
	}

	console.LogDebug("All preferred interfaces failed, falling back to default system routing")
	// Fallback to default route
	return GetPublicIP(ctx)
}
