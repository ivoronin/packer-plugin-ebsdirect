// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// builderID identifies artifacts produced by this post-processor.
const builderID = "ivoronin.post-processor.ebsdirect"

// amiArtifact is the registered AMI. Destroy deregisters it and deletes the snapshot.
type amiArtifact struct {
	region     string
	amiID      string
	snapshotID string
	destroyer  imageDestroyer
}

func (a *amiArtifact) BuilderId() string        { return builderID }
func (a *amiArtifact) Files() []string          { return nil }
func (a *amiArtifact) Id() string               { return fmt.Sprintf("%s:%s", a.region, a.amiID) }
func (a *amiArtifact) String() string           { return fmt.Sprintf("AMI %s in %s", a.amiID, a.region) }
func (a *amiArtifact) State(string) interface{} { return nil }

// Destroy tears down whatever the run created, in reverse order. It doubles as
// the rollback ladder: run builds the artifact right after the upload (snapshot
// only, amiID empty) and fills in amiID after RegisterImage, so a Destroy with
// no amiID unwinds a partial run by deleting just the orphan snapshot. It is
// best-effort: it attempts the snapshot delete even if the deregister fails, so
// a failed DeregisterImage never leaves the snapshot orphaned, and joins
// whatever errors came back. context.Background is deliberate so teardown runs
// even when the request ctx that drove the run was cancelled.
func (a *amiArtifact) Destroy() error {
	var errs []error
	if a.amiID != "" {
		if _, err := a.destroyer.DeregisterImage(context.Background(), &ec2.DeregisterImageInput{
			ImageId: aws.String(a.amiID),
		}); err != nil {
			errs = append(errs, fmt.Errorf("deregister %s: %w", a.amiID, err))
		}
	}
	if _, err := a.destroyer.DeleteSnapshot(context.Background(), &ec2.DeleteSnapshotInput{
		SnapshotId: aws.String(a.snapshotID),
	}); err != nil {
		errs = append(errs, fmt.Errorf("delete snapshot %s: %w", a.snapshotID, err))
	}
	return errors.Join(errs...)
}
