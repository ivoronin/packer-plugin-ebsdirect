// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"errors"
	"testing"
)

func TestConfigureDefaults(t *testing.T) {
	var p PostProcessor
	if err := p.Configure(map[string]interface{}{"ami_name": "img-1"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	c := p.config
	if c.Architecture != "x86_64" || c.BootMode != "legacy-bios" ||
		c.RootDeviceName != "/dev/xvda" {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		c    Config
		ok   bool
	}{
		{"missing name", Config{Architecture: "x86_64", BootMode: "legacy-bios"}, false},
		{"bad arch", Config{AMIName: "a", Architecture: "ppc", BootMode: "legacy-bios"}, false},
		{"bad bootmode", Config{AMIName: "a", Architecture: "x86_64", BootMode: "bios"}, false},
		{"valid", Config{AMIName: "a", Architecture: "arm64", BootMode: "uefi"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
	if !errors.Is((&Config{Architecture: "x86_64", BootMode: "uefi"}).Validate(), errNoAMIName) {
		t.Fatal("missing ami_name must return errNoAMIName")
	}
}
