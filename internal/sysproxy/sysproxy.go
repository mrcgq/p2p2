// internal/sysproxy/sysproxy.go

package sysproxy

// Enable 启用系统代理
func Enable(proxyAddr string) error {
	return enableProxy(proxyAddr)
}

// Disable 禁用系统代理
func Disable() error {
	return disableProxy()
}

// IsEnabled 检查系统代理是否启用
func IsEnabled() (bool, error) {
	return isProxyEnabled()
}
