// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"context"
	"errors"
	"fmt"
	"io"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ebs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type awsDeps struct {
	writer    snapshotWriter
	waiter    snapshotWaiter
	registrar imageRegistrar
	sharer    imageSharer
	destroyer imageDestroyer
	region    string
}

// run uploads the image to a snapshot, registers an AMI, then shares its launch
// permission. The artifact doubles as the rollback ladder: it is built right
// after the upload and gains the AMI id after register, so on any step failure
// art.Destroy() unwinds however far the run got, its error joined to the
// returned one. The teardown context policy lives in Destroy.
func run(ctx context.Context, deps awsDeps, cfg Config, src io.ReaderAt, size int64) (*amiArtifact, error) {
	snapshotID, err := upload(ctx, deps.writer, deps.waiter, uploadInput{
		Source:      src,
		SizeBytes:   size,
		Description: cfg.AMIDescription,
		Tags:        cfg.SnapshotTags,
		Encrypt:     cfg.Encrypt,
		KMSKey:      cfg.KMSKey,
	})
	if err != nil {
		return nil, err
	}

	// The artifact is the rollback ladder: it carries the snapshot now and gains
	// the AMI id after register, so art.Destroy() unwinds however far run got.
	art := &amiArtifact{region: deps.region, snapshotID: snapshotID, destroyer: deps.destroyer}

	amiID, err := register(ctx, deps.registrar, registerInput{
		SnapshotID:     snapshotID,
		Name:           cfg.AMIName,
		Description:    cfg.AMIDescription,
		Architecture:   cfg.Architecture,
		RootDeviceName: cfg.RootDeviceName,
		BootMode:       cfg.BootMode,
		IMDSSupport:    cfg.IMDSSupport,
		Tags:           cfg.Tags,
	})
	if err != nil {
		return nil, errors.Join(err, art.Destroy()) //nolint:contextcheck // Destroy uses context.Background (packer.Artifact.Destroy takes no ctx) so teardown runs even if the request ctx is cancelled
	}
	art.amiID = amiID

	if err := share(ctx, deps.sharer, amiID, shareInput{
		Users:   cfg.AMIUsers,
		Groups:  cfg.AMIGroups,
		OrgArns: cfg.AMIOrgArns,
		OuArns:  cfg.AMIOuArns,
	}); err != nil {
		return nil, errors.Join(err, art.Destroy()) //nolint:contextcheck // Destroy uses context.Background (packer.Artifact.Destroy takes no ctx) so teardown runs even if the request ctx is cancelled
	}
	return art, nil
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
		sharer:    ec2c,
		destroyer: ec2c,
		region:    cfg.Region,
	}, nil
}
