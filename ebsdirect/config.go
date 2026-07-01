// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

//go:generate packer-sdc mapstructure-to-hcl2 -type Config
package ebsdirect

import (
	"errors"
	"fmt"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
)

// Config is the HCL configuration of the ebsdirect post-processor.
type Config struct {
	AMIName        string            `mapstructure:"ami_name"`
	AMIDescription string            `mapstructure:"ami_description"`
	Architecture   string            `mapstructure:"architecture"`
	BootMode       string            `mapstructure:"boot_mode"`
	RootDeviceName string            `mapstructure:"root_device_name"`
	Region         string            `mapstructure:"region"`
	Tags           map[string]string `mapstructure:"tags"`
	SnapshotTags   map[string]string `mapstructure:"snapshot_tags"`
	Encrypt        bool              `mapstructure:"ami_encrypt"`
	KMSKey         string            `mapstructure:"ami_kms_key"`
	IMDSSupport    string            `mapstructure:"imds_support"`
	AMIUsers       []string          `mapstructure:"ami_users"`
	AMIGroups      []string          `mapstructure:"ami_groups"`
	AMIOrgArns     []string          `mapstructure:"ami_org_arns"`
	AMIOuArns      []string          `mapstructure:"ami_ou_arns"`
}

const (
	defaultArchitecture   = "x86_64"
	defaultBootMode       = "legacy-bios"
	defaultRootDeviceName = "/dev/xvda"
)

var errNoAMIName = errors.New("ami_name is required")

func (c *Config) applyDefaults() {
	if c.Architecture == "" {
		c.Architecture = defaultArchitecture
	}
	if c.BootMode == "" {
		c.BootMode = defaultBootMode
	}
	if c.RootDeviceName == "" {
		c.RootDeviceName = defaultRootDeviceName
	}
}

// Validate checks HCL field values (not the image size; that is checked at
// PostProcess time against the file).
func (c *Config) Validate() error {
	if c.AMIName == "" {
		return errNoAMIName
	}
	switch c.Architecture {
	case "x86_64", "arm64":
	default:
		return fmt.Errorf("architecture must be x86_64 or arm64, got %q", c.Architecture)
	}
	switch c.BootMode {
	case "legacy-bios", "uefi", "uefi-preferred":
	default:
		return fmt.Errorf("boot_mode must be legacy-bios, uefi, or uefi-preferred, got %q", c.BootMode)
	}
	switch c.IMDSSupport {
	case "", "v2.0":
	default:
		return fmt.Errorf("imds_support must be empty or v2.0, got %q", c.IMDSSupport)
	}
	for _, g := range c.AMIGroups {
		if g != "all" {
			return fmt.Errorf("ami_groups only supports %q, got %q", "all", g)
		}
		if c.Encrypt {
			return errors.New(`ami_encrypt cannot be combined with ami_groups = ["all"]: an encrypted AMI cannot be made public`)
		}
	}
	for _, f := range []struct {
		name string
		vals []string
	}{
		{"ami_users", c.AMIUsers},
		{"ami_org_arns", c.AMIOrgArns},
		{"ami_ou_arns", c.AMIOuArns},
	} {
		for _, v := range f.vals {
			if v == "" {
				return fmt.Errorf("%s contains an empty entry", f.name)
			}
		}
	}
	return nil
}

// kmsKeyIgnored reports whether an ami_kms_key was set while ami_encrypt is off,
// in which case the key is dropped (parity with amazon-import). PostProcess
// warns on this; the engine gate in kmsKeyArn enforces it.
func (c *Config) kmsKeyIgnored() bool {
	return c.KMSKey != "" && !c.Encrypt
}

func (p *PostProcessor) Configure(raws ...interface{}) error {
	if err := config.Decode(&p.config, nil, raws...); err != nil {
		return err
	}
	p.config.applyDefaults()
	return p.config.Validate()
}

func (p *PostProcessor) ConfigSpec() hcldec.ObjectSpec {
	return p.config.FlatMapstructure().HCL2Spec()
}
