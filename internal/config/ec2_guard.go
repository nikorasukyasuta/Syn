package config

import (
	"os"
	"strings"
)

const (
	// EnvironmentProduction is the default runtime mode for EC2 production deployments.
	EnvironmentProduction = "production"
	// EnvironmentDevelopment is the local-only development mode (forbidden on AWS EC2).
	EnvironmentDevelopment = "development"
)

// NormalizeEnvironment normalizes raw environment strings ("prod", "production", "dev", "development", "local")
// into canonical values ("production", "development") or returns the lowercase trimmed input if unrecognized.
func NormalizeEnvironment(raw string) string {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	switch cleaned {
	case "", "prod", "production":
		return EnvironmentProduction
	case "dev", "development", "local":
		return EnvironmentDevelopment
	default:
		return cleaned
	}
}

// EC2Detector inspects Linux DMI/SMBIOS, hypervisor UUID, cloud-init metadata, and environment markers
// to determine whether the current host is an AWS EC2 instance.
type EC2Detector struct {
	DMIProductUUIDPath string
	DMIVendorPaths     []string
	HypervisorUUIDPath string
	CloudInitIDPath    string
	Getenv             func(string) string
}

// DefaultEC2Detector returns an EC2Detector configured with standard Linux kernel and cloud-init paths.
func DefaultEC2Detector() *EC2Detector {
	return &EC2Detector{
		DMIProductUUIDPath: "/sys/devices/virtual/dmi/id/product_uuid",
		DMIVendorPaths: []string{
			"/sys/devices/virtual/dmi/id/board_vendor",
			"/sys/devices/virtual/dmi/id/sys_vendor",
			"/sys/devices/virtual/dmi/id/bios_vendor",
			"/sys/devices/virtual/dmi/id/product_version",
			"/sys/devices/virtual/dmi/id/ chassis_asset_tag",
			"/sys/devices/virtual/dmi/id/chassis_asset_tag",
		},
		HypervisorUUIDPath: "/sys/hypervisor/uuid",
		CloudInitIDPath:    "/var/lib/cloud/data/instance-id",
		Getenv:             os.Getenv,
	}
}

// IsEC2 returns true if the host environment matches AWS EC2 hardware/hypervisor signatures.
func (d *EC2Detector) IsEC2() bool {
	getenv := d.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	// 1. Check explicit EC2/AWS environment markers
	if strings.TrimSpace(getenv("EC2_INSTANCE_ID")) != "" {
		return true
	}
	if strings.Contains(strings.ToLower(getenv("AWS_EXECUTION_ENV")), "ec2") {
		return true
	}

	// 2. Check DMI product UUID (starts with "ec2" on Nitro and Xen EC2 instances)
	if hasPrefixInFile(d.DMIProductUUIDPath, "ec2") {
		return true
	}

	// 3. Check hypervisor UUID (starts with "ec2" on Xen EC2 instances)
	if hasPrefixInFile(d.HypervisorUUIDPath, "ec2") {
		return true
	}

	// 4. Check DMI board/system/bios vendor strings for "amazon ec2"
	for _, p := range d.DMIVendorPaths {
		if containsInFile(p, "amazon ec2") {
			return true
		}
	}

	// 5. Check cloud-init instance-id (e.g., "i-0123456789abcdef0")
	if hasPrefixInFile(d.CloudInitIDPath, "i-") {
		return true
	}

	return false
}

// IsRunningOnEC2 returns true when the current process is executing on an AWS EC2 instance.
func IsRunningOnEC2() bool {
	return DefaultEC2Detector().IsEC2()
}

func hasPrefixInFile(path, prefix string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(string(data)))
	return strings.HasPrefix(content, strings.ToLower(prefix))
}

func containsInFile(path, substr string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(string(data)))
	return strings.Contains(content, strings.ToLower(substr))
}

