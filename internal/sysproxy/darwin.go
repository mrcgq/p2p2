
//go:build darwin

package sysproxy

import (
	"fmt"
	"os/exec"
	"strings"
)

func enableDarwin(proxyAddr string) error {
	parts := strings.Split(proxyAddr, ":")
	if len(parts) != 2 {
		return fmt.Errorf("无效的代理地址")
	}
	host, port := parts[0], parts[1]

	services, err := getNetworkServices()
	if err != nil {
		return err
	}

	for _, service := range services {
		exec.Command("networksetup", "-setwebproxy", service, host, port).Run()
		exec.Command("networksetup", "-setwebproxystate", service, "on").Run()
		exec.Command("networksetup", "-setsecurewebproxy", service, host, port).Run()
		exec.Command("networksetup", "-setsecurewebproxystate", service, "on").Run()
		exec.Command("networksetup", "-setsocksfirewallproxy", service, host, port).Run()
		exec.Command("networksetup", "-setsocksfirewallproxystate", service, "on").Run()
	}

	return nil
}

func disableDarwin() error {
	services, err := getNetworkServices()
	if err != nil {
		return err
	}

	for _, service := range services {
		exec.Command("networksetup", "-setwebproxystate", service, "off").Run()
		exec.Command("networksetup", "-setsecurewebproxystate", service, "off").Run()
		exec.Command("networksetup", "-setsocksfirewallproxystate", service, "off").Run()
	}

	return nil
}

func isEnabledDarwin() (bool, error) {
	services, err := getNetworkServices()
	if err != nil || len(services) == 0 {
		return false, err
	}

	output, err := exec.Command("networksetup", "-getwebproxy", services[0]).Output()
	if err != nil {
		return false, err
	}

	return strings.Contains(string(output), "Enabled: Yes"), nil
}

func getNetworkServices() ([]string, error) {
	output, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(output), "\n")
	var services []string
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "*") {
			services = append(services, line)
		}
	}

	return services, nil
}


