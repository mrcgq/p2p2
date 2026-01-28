// internal/sysproxy/sysproxy_windows.go

//go:build windows

package sysproxy

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

var (
	wininet                       = syscall.NewLazyDLL("wininet.dll")
	internetSetOption             = wininet.NewProc("InternetSetOptionW")
	internetOptionRefresh         = 37
	internetOptionSettingsChanged = 39
)

func enableProxy(proxyAddr string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表: %w", err)
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}

	if err := key.SetStringValue("ProxyServer", proxyAddr); err != nil {
		return err
	}

	if err := key.SetStringValue("ProxyOverride", "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*"); err != nil {
		return err
	}

	return refreshSettings()
}

func disableProxy() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表: %w", err)
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
		return err
	}

	return refreshSettings()
}

func isProxyEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer key.Close()

	val, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil {
		return false, nil
	}

	return val == 1, nil
}

func refreshSettings() error {
	internetSetOption.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	internetSetOption.Call(0, uintptr(internetOptionRefresh), 0, 0)
	return nil
}
