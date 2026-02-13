// Package discovery handles mDNS service registration for local network discovery.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

const Hostname = "skaldi"

func Register(ctx context.Context, logger *slog.Logger, port int) (cleanup func(), ok bool) {
	ip := primaryLANIP()
	if ip == "" {
		logger.Warn("No LAN IP found, skipping mDNS registration")
		return func() {}, false
	}

	switch runtime.GOOS {
	case "linux":
		return registerAvahi(ctx, logger, ip, port)
	case "darwin":
		return registerBonjour(ctx, logger, ip, port)
	case "windows":
		return registerWindows(ctx, logger, ip, port)
	default:
		logger.Warn("mDNS not supported on this platform", "os", runtime.GOOS)
		return func() {}, false
	}
}

func registerAvahi(ctx context.Context, logger *slog.Logger, ip string, port int) (cleanup func(), ok bool) {
	path, err := exec.LookPath("avahi-publish-service")
	if err != nil {
		path, err = exec.LookPath("avahi-publish")
		if err != nil {
			logger.Warn("avahi-publish-service not found, mDNS unavailable (install avahi-utils)")
			return func() {}, false
		}
		logger.Debug("Falling back to avahi-publish for address-only registration")
		cmd := exec.CommandContext(ctx, path, "-a", "-R", Hostname+".local", ip)
		if err := cmd.Start(); err != nil {
			logger.Warn("Failed to start avahi-publish", "error", err)
			return func() {}, false
		}
		return func() {
			cmd.Process.Signal(os.Interrupt)
			cmd.Wait()
		}, true
	}

	fqdn := Hostname + ".local"

	addrCmd := exec.CommandContext(ctx, path, "-a", "-R", fqdn, ip)
	if err := addrCmd.Start(); err != nil {
		logger.Warn("Failed to publish address record", "error", err)
		return func() {}, false
	}

	svcCmd := exec.CommandContext(ctx, path, "-s", "-H", fqdn, "Skaldi Jukebox", "_http._tcp", fmt.Sprint(port), "path=/")
	if err := svcCmd.Start(); err != nil {
		addrCmd.Process.Signal(os.Interrupt)
		addrCmd.Wait()
		logger.Warn("Failed to publish service record", "error", err)
		return func() {}, false
	}

	logger.Debug("mDNS address and service registration started")
	return func() {
		svcCmd.Process.Signal(os.Interrupt)
		svcCmd.Wait()
		addrCmd.Process.Signal(os.Interrupt)
		addrCmd.Wait()
	}, true
}

func registerBonjour(ctx context.Context, logger *slog.Logger, ip string, port int) (cleanup func(), ok bool) {
	path, err := exec.LookPath("dns-sd")
	if err != nil {
		logger.Warn("dns-sd not found, mDNS unavailable")
		return func() {}, false
	}

	cmd := exec.CommandContext(ctx, path, "-P", "Skaldi Jukebox",
		"_http._tcp", "local", fmt.Sprintf("%d", port),
		Hostname+".local", ip)

	if err := cmd.Start(); err != nil {
		logger.Warn("Failed to start dns-sd", "error", err)
		return func() {}, false
	}

	logger.Debug("mDNS service registration started")
	return func() {
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
	}, true
}

func registerWindows(ctx context.Context, logger *slog.Logger, ip string, port int) (cleanup func(), ok bool) {
	if _, err := exec.LookPath("dns-sd"); err != nil {
		logger.Warn("dns-sd not found (install Bonjour SDK), mDNS unavailable")
		return func() {}, false
	}
	return registerBonjour(ctx, logger, ip, port)
}

func primaryLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	sort.Slice(ifaces, func(i, j int) bool {
		return interfacePriority(ifaces[i].Name) > interfacePriority(ifaces[j].Name)
	})

	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := i.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			if ip.IsLinkLocalUnicast() {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

func interfacePriority(name string) int {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "wlan"):
		return 100
	case strings.HasPrefix(lower, "eth"):
		return 90
	case strings.HasPrefix(lower, "wl"):
		return 80
	case strings.HasPrefix(lower, "en"):
		return 70
	default:
		return 0
	}
}
