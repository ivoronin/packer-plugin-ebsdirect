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

// shareInput lists the principals to grant AMI launch permission.
type shareInput struct {
	Users   []string
	Groups  []string
	OrgArns []string
	OuArns  []string
}

// share grants launch permission on amiID to every principal in in, in one
// ModifyImageAttribute call. The referenced snapshot is not shared: AWS provides
// the recipient snapshot access for launch; sharing the snapshot is only needed
// to copy the AMI. It is a no-op when in carries no principals.
func share(ctx context.Context, s imageSharer, amiID string, in shareInput) error {
	perms := make([]ec2types.LaunchPermission, 0, len(in.Users)+len(in.Groups)+len(in.OrgArns)+len(in.OuArns))
	for _, u := range in.Users {
		perms = append(perms, ec2types.LaunchPermission{UserId: aws.String(u)})
	}
	for _, g := range in.Groups {
		perms = append(perms, ec2types.LaunchPermission{Group: ec2types.PermissionGroup(g)})
	}
	for _, o := range in.OrgArns {
		perms = append(perms, ec2types.LaunchPermission{OrganizationArn: aws.String(o)})
	}
	for _, o := range in.OuArns {
		perms = append(perms, ec2types.LaunchPermission{OrganizationalUnitArn: aws.String(o)})
	}
	if len(perms) == 0 {
		return nil
	}
	if _, err := s.ModifyImageAttribute(ctx, &ec2.ModifyImageAttributeInput{
		ImageId:          aws.String(amiID),
		Attribute:        aws.String("launchPermission"),
		LaunchPermission: &ec2types.LaunchPermissionModifications{Add: perms},
	}); err != nil {
		return fmt.Errorf("share image %s: %w", amiID, err)
	}
	return nil
}
