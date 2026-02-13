package discovery

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const Hostname = "skaldi"

func Register(ctx context.Context, logger *slog.Logger, port int) (cleanup func(), ok bool) {
	ip := primaryLANIP()
	if ip == "" {
		logger.Debug("No LAN IP found, skipping mDNS registration")
		return func() {}, false
	}

	switch runtime.GOOS {
	case "linux":
		return registerAvahi(ctx, logger, ip, port)
	case "darwin":
		return registerBonjour(ctx, logger, ip, port)
	default:
		return func() {}, false
	}
}

func registerAvahi(ctx context.Context, logger *slog.Logger, ip string, port int) (cleanup func(), ok bool) {
	path, err := exec.LookPath("avahi-publish")
	if err != nil {
		logger.Debug("avahi-publish not found, skipping mDNS")
		return func() {}, false
	}

	cmd := exec.CommandContext(ctx, path, "-a", "-R", Hostname+".local", ip)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Debug("Failed to get stdout pipe", "error", err)
		return func() {}, false
	}

	if err := cmd.Start(); err != nil {
		logger.Debug("Failed to start avahi-publish", "error", err)
		return func() {}, false
	}

	scanner := bufio.NewScanner(stdout)
	established := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "Established") {
				established <- true
				return
			}
		}
		established <- false
	}()

	select {
	case <-established:
		logger.Debug("mDNS registration established")
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		return func() {}, false
	}

	return func() {
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
	}, true
}

func registerBonjour(ctx context.Context, logger *slog.Logger, ip string, port int) (cleanup func(), ok bool) {
	path, err := exec.LookPath("dns-sd")
	if err != nil {
		logger.Debug("dns-sd not found, skipping mDNS")
		return func() {}, false
	}

	cmd := exec.CommandContext(ctx, path, "-P", "Skaldi Jukebox",
		"_http._tcp", "local", fmt.Sprintf("%d", port),
		Hostname+".local", ip)

	if err := cmd.Start(); err != nil {
		logger.Debug("Failed to start dns-sd", "error", err)
		return func() {}, false
	}

	return func() {
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
	}, true
}

func primaryLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

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
