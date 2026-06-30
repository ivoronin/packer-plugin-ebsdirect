// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/packer-plugin-sdk/packer"

	"github.com/ivoronin/packer-plugin-ebsdirect/internal/blocks"
)

// PostProcessor uploads a raw disk image to an EBS snapshot and registers an AMI.
type PostProcessor struct {
	config Config
}

func (p *PostProcessor) PostProcess(ctx context.Context, ui packer.Ui, artifact packer.Artifact) (packer.Artifact, bool, bool, error) {
	if p.config.kmsKeyIgnored() {
		ui.Say("ebsdirect: ignoring ami_kms_key because ami_encrypt is false")
	}
	path, err := pickSourceFile(artifact)
	if err != nil {
		return nil, false, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, false, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := blocks.Validate(info.Size()); err != nil {
		return nil, false, false, err
	}

	f, err := os.Open(path) //nolint:gosec // G304: path is the raw-image artifact produced by the upstream builder
	if err != nil {
		return nil, false, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	deps, err := buildDeps(ctx, p.config.Region)
	if err != nil {
		return nil, false, false, err
	}

	ui.Say(fmt.Sprintf("ebsdirect: uploading %s (%d GiB) to an EBS snapshot", path, blocks.VolumeSizeGiB(info.Size())))
	art, err := run(ctx, deps, p.config, f, info.Size())
	if err != nil {
		return nil, false, false, err
	}
	ui.Say("ebsdirect: registered " + art.Id())
	return art, false, false, nil
}

func pickSourceFile(artifact packer.Artifact) (string, error) {
	const suffix = ".raw"
	var matches []string
	for _, f := range artifact.Files() {
		if strings.HasSuffix(f, suffix) {
			matches = append(matches, f)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no %s file in input artifact (%v)", suffix, artifact.Files())
	default:
		return "", fmt.Errorf("multiple %s files in input artifact: %v", suffix, matches)
	}
}
