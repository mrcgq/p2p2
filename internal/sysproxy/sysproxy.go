
package sysproxy

import "runtime"

// Enable 启用系统代理
func Enable(proxyAddr string) error {
	switch runtime.GOOS {
	case "windows":
		return enableWindows(proxyAddr)
	case "darwin":
		return enableDarwin(proxyAddr)
	case "linux":
		return enableLinux(proxyAddr)
	default:
		return nil
	}
}

// Disable 禁用系统代理
func Disable() error {
	switch runtime.GOOS {
	case "windows":
		return disableWindows()
	case "darwin":
		return disableDarwin()
	case "linux":
		return disableLinux()
	default:
		return nil
	}
}

// IsEnabled 检查系统代理是否启用
func IsEnabled() (bool, error) {
	switch runtime.GOOS {
	case "windows":
		return isEnabledWindows()
	case "darwin":
		return isEnabledDarwin()
	case "linux":
		return isEnabledLinux()
	default:
		return false, nil
	}
}

