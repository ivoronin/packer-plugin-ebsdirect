// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ebs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// snapshotWriter is the EBS direct write side used by the uploader.
// Satisfied by *ebs.Client.
type snapshotWriter interface {
	StartSnapshot(context.Context, *ebs.StartSnapshotInput, ...func(*ebs.Options)) (*ebs.StartSnapshotOutput, error)
	PutSnapshotBlock(context.Context, *ebs.PutSnapshotBlockInput, ...func(*ebs.Options)) (*ebs.PutSnapshotBlockOutput, error)
	CompleteSnapshot(context.Context, *ebs.CompleteSnapshotInput, ...func(*ebs.Options)) (*ebs.CompleteSnapshotOutput, error)
}

// snapshotWaiter polls snapshot state. Satisfied by *ec2.Client.
type snapshotWaiter interface {
	DescribeSnapshots(context.Context, *ec2.DescribeSnapshotsInput, ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
}

// imageRegistrar registers an AMI from a snapshot. Satisfied by *ec2.Client.
type imageRegistrar interface {
	RegisterImage(context.Context, *ec2.RegisterImageInput, ...func(*ec2.Options)) (*ec2.RegisterImageOutput, error)
}

// imageDestroyer tears down an AMI and snapshot. Satisfied by *ec2.Client.
type imageDestroyer interface {
	DeregisterImage(context.Context, *ec2.DeregisterImageInput, ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error)
	DeleteSnapshot(context.Context, *ec2.DeleteSnapshotInput, ...func(*ec2.Options)) (*ec2.DeleteSnapshotOutput, error)
}
