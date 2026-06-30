// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

// Command packer-plugin-ebsdirect uploads a raw disk image to an EBS snapshot
// via the EBS direct APIs and registers it as an AMI.
package main

import (
	"fmt"
	"os"

	"github.com/hashicorp/packer-plugin-sdk/plugin"
	"github.com/ivoronin/packer-plugin-ebsdirect/ebsdirect"
	"github.com/ivoronin/packer-plugin-ebsdirect/version"
)

func main() {
	pps := plugin.NewSet()
	pps.RegisterPostProcessor(plugin.DEFAULT_NAME, new(ebsdirect.PostProcessor))
	pps.SetVersion(version.PluginVersion)

	if err := pps.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
