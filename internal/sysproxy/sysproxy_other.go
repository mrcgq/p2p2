// internal/sysproxy/sysproxy_other.go

//go:build !windows && !darwin && !linux

package sysproxy

func enableProxy(proxyAddr string) error {
	return nil
}

func disableProxy() error {
	return nil
}

func isProxyEnabled() (bool, error) {
	return false, nil
}
