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

type fakeSharer struct {
	got *ec2.ModifyImageAttributeInput
	err error
}

func (f *fakeSharer) ModifyImageAttribute(_ context.Context, in *ec2.ModifyImageAttributeInput, _ ...func(*ec2.Options)) (*ec2.ModifyImageAttributeOutput, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return &ec2.ModifyImageAttributeOutput{}, nil
}

func TestShareAllPrincipals(t *testing.T) {
	s := &fakeSharer{}
	err := share(context.Background(), s, "ami-1", shareInput{
		Users:   []string{"111111111111"},
		Groups:  []string{"all"},
		OrgArns: []string{"arn:aws:organizations::111111111111:organization/o-abc"},
		OuArns:  []string{"arn:aws:organizations::111111111111:ou/o-abc/ou-xyz"},
	})
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if aws.ToString(s.got.ImageId) != "ami-1" || aws.ToString(s.got.Attribute) != "launchPermission" {
		t.Fatalf("bad ModifyImageAttribute target: %+v", s.got)
	}
	add := s.got.LaunchPermission.Add
	if len(add) != 4 {
		t.Fatalf("want 4 permissions, got %d", len(add))
	}
	var user, group, org, ou bool
	for _, p := range add {
		switch {
		case aws.ToString(p.UserId) == "111111111111":
			user = true
		case p.Group == ec2types.PermissionGroupAll:
			group = true
		case aws.ToString(p.OrganizationArn) != "":
			org = true
		case aws.ToString(p.OrganizationalUnitArn) != "":
			ou = true
		}
	}
	if !user || !group || !org || !ou {
		t.Fatalf("missing principal: user=%v group=%v org=%v ou=%v", user, group, org, ou)
	}
}

func TestShareNoop(t *testing.T) {
	s := &fakeSharer{}
	if err := share(context.Background(), s, "ami-1", shareInput{}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if s.got != nil {
		t.Fatal("ModifyImageAttribute must not be called with no principals")
	}
}
