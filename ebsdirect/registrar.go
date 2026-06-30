// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// registerInput describes the AMI to register from a completed snapshot.
type registerInput struct {
	SnapshotID     string
	Name           string
	Description    string
	Architecture   string
	RootDeviceName string
	BootMode       string
	Tags           map[string]string
}

// register registers a modern HVM AMI backed by the given snapshot and returns
// the AMI id. VirtualizationType, ENA and gp3/DeleteOnTermination are fixed.
func register(ctx context.Context, r imageRegistrar, in registerInput) (string, error) {
	input := &ec2.RegisterImageInput{
		Name:               aws.String(in.Name),
		Architecture:       ec2types.ArchitectureValues(in.Architecture),
		RootDeviceName:     aws.String(in.RootDeviceName),
		VirtualizationType: aws.String("hvm"),
		EnaSupport:         aws.Bool(true),
		BootMode:           ec2types.BootModeValues(in.BootMode),
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{{
			DeviceName: aws.String(in.RootDeviceName),
			Ebs: &ec2types.EbsBlockDevice{
				SnapshotId:          aws.String(in.SnapshotID),
				VolumeType:          ec2types.VolumeTypeGp3,
				DeleteOnTermination: aws.Bool(true),
			},
		}},
	}
	input.Description = optionalString(in.Description)
	if len(in.Tags) > 0 {
		input.TagSpecifications = []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeImage,
			Tags:         ec2Tags(in.Tags),
		}}
	}
	out, err := r.RegisterImage(ctx, input)
	if err != nil {
		return "", fmt.Errorf("register image: %w", err)
	}
	return aws.ToString(out.ImageId), nil
}

func ec2Tags(m map[string]string) []ec2types.Tag {
	if len(m) == 0 {
		return nil
	}
	tags := make([]ec2types.Tag, 0, len(m))
	for k, v := range m {
		tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return tags
}
