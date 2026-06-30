// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

// Package version defines the plugin version, overridable at build time via ldflags.
package version

import "github.com/hashicorp/packer-plugin-sdk/version"

var (
	Version           = "0.1.0"
	VersionPrerelease = ""
	VersionMetadata   = ""

	PluginVersion = version.NewPluginVersion(Version, VersionPrerelease, VersionMetadata)
)
