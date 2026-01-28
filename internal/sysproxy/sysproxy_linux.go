// internal/sysproxy/sysproxy_linux.go

//go:build linux

package sysproxy

import (
	"os"
	"os/exec"
	"strings"
)

func enableProxy(proxyAddr string) error {
	os.Setenv("http_proxy", "http://"+proxyAddr)
	os.Setenv("https_proxy", "http://"+proxyAddr)
	os.Setenv("HTTP_PROXY", "http://"+proxyAddr)
	os.Setenv("HTTPS_PROXY", "http://"+proxyAddr)

	parts := strings.Split(proxyAddr, ":")
	if len(parts) == 2 {
		exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "manual").Run()
		exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "host", parts[0]).Run()
		exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "port", parts[1]).Run()
		exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "host", parts[0]).Run()
		exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "port", parts[1]).Run()
	}

	return nil
}

func disableProxy() error {
	os.Unsetenv("http_proxy")
	os.Unsetenv("https_proxy")
	os.Unsetenv("HTTP_PROXY")
	os.Unsetenv("HTTPS_PROXY")

	exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "none").Run()

	return nil
}

func isProxyEnabled() (bool, error) {
	if os.Getenv("http_proxy") != "" || os.Getenv("HTTP_PROXY") != "" {
		return true, nil
	}

	output, err := exec.Command("gsettings", "get", "org.gnome.system.proxy", "mode").Output()
	if err != nil {
		return false, nil
	}

	return strings.Contains(string(output), "manual"), nil
}
