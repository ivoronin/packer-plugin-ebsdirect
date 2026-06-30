// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type fakeRegistrar struct {
	got *ec2.RegisterImageInput
	err error
}

func (f *fakeRegistrar) RegisterImage(_ context.Context, in *ec2.RegisterImageInput, _ ...func(*ec2.Options)) (*ec2.RegisterImageOutput, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return &ec2.RegisterImageOutput{ImageId: aws.String("ami-test")}, nil
}

func TestRegister(t *testing.T) {
	r := &fakeRegistrar{}
	id, err := register(context.Background(), r, registerInput{
		SnapshotID: "snap-1", Name: "img", Architecture: "x86_64",
		RootDeviceName: "/dev/xvda", BootMode: "legacy-bios",
		Tags: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if id != "ami-test" {
		t.Fatalf("want ami-test, got %s", id)
	}
	in := r.got
	if in.Architecture != ec2types.ArchitectureValuesX8664 {
		t.Fatalf("architecture: %v", in.Architecture)
	}
	if aws.ToString(in.VirtualizationType) != "hvm" || !aws.ToBool(in.EnaSupport) {
		t.Fatal("must be hvm with ENA")
	}
	if len(in.BlockDeviceMappings) != 1 {
		t.Fatalf("want 1 mapping, got %d", len(in.BlockDeviceMappings))
	}
	ebsm := in.BlockDeviceMappings[0].Ebs
	if aws.ToString(ebsm.SnapshotId) != "snap-1" || ebsm.VolumeType != ec2types.VolumeTypeGp3 || !aws.ToBool(ebsm.DeleteOnTermination) {
		t.Fatalf("bad ebs mapping: %+v", ebsm)
	}
	if len(in.TagSpecifications) != 1 || in.TagSpecifications[0].ResourceType != ec2types.ResourceTypeImage {
		t.Fatal("ami tags must be applied via TagSpecifications/image")
	}
}
