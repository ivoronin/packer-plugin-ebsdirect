// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ebs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type awsDeps struct {
	writer    snapshotWriter
	waiter    snapshotWaiter
	registrar imageRegistrar
	destroyer imageDestroyer
	region    string
}

// run uploads the image to a snapshot, registers an AMI, and on registration
// failure best-effort deletes the orphaned snapshot, joining any cleanup error.
func run(ctx context.Context, deps awsDeps, cfg Config, src io.ReaderAt, size int64) (*amiArtifact, error) {
	snapshotID, err := upload(ctx, deps.writer, deps.waiter, uploadInput{
		Source:      src,
		SizeBytes:   size,
		Description: cfg.AMIDescription,
		Tags:        cfg.SnapshotTags,
	})
	if err != nil {
		return nil, err
	}

	amiID, err := register(ctx, deps.registrar, registerInput{
		SnapshotID:     snapshotID,
		Name:           cfg.AMIName,
		Description:    cfg.AMIDescription,
		Architecture:   cfg.Architecture,
		RootDeviceName: cfg.RootDeviceName,
		BootMode:       cfg.BootMode,
		Tags:           cfg.Tags,
	})
	if err != nil {
		if _, delErr := deps.destroyer.DeleteSnapshot(ctx, &ec2.DeleteSnapshotInput{
			SnapshotId: aws.String(snapshotID),
		}); delErr != nil {
			return nil, errors.Join(err, fmt.Errorf("cleanup snapshot %s: %w", snapshotID, delErr))
		}
		return nil, err
	}

	return &amiArtifact{region: deps.region, amiID: amiID, snapshotID: snapshotID, destroyer: deps.destroyer}, nil
}

var errNoRegion = errors.New("no AWS region; set the region field or AWS_REGION")

// requireRegion reports an error when no region was resolved from the config
// field or the default chain.
func requireRegion(resolved string) error {
	if resolved == "" {
		return errNoRegion
	}
	return nil
}

// buildDeps loads the AWS config (honoring an explicit region, else the default
// chain) with a connection pool sized to the upload concurrency, and wires the
// EBS/EC2 clients into awsDeps.
func buildDeps(ctx context.Context, region string) (awsDeps, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithHTTPClient(tunedHTTPClient()),
	}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return awsDeps{}, fmt.Errorf("load aws config: %w", err)
	}
	if err := requireRegion(cfg.Region); err != nil {
		return awsDeps{}, err
	}
	ec2c := ec2.NewFromConfig(cfg)
	return awsDeps{
		writer:    ebs.NewFromConfig(cfg),
		waiter:    ec2c,
		registrar: ec2c,
		destroyer: ec2c,
		region:    cfg.Region,
	}, nil
}
