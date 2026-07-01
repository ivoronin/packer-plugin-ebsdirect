// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type fakeDestroyer struct {
	deregistered, deletedSnap string
	deregCalled               bool
	deregErr, delErr          error
}

func (f *fakeDestroyer) DeregisterImage(_ context.Context, in *ec2.DeregisterImageInput, _ ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error) {
	f.deregCalled = true
	f.deregistered = aws.ToString(in.ImageId)
	if f.deregErr != nil {
		return nil, f.deregErr
	}
	return &ec2.DeregisterImageOutput{}, nil
}

func (f *fakeDestroyer) DeleteSnapshot(_ context.Context, in *ec2.DeleteSnapshotInput, _ ...func(*ec2.Options)) (*ec2.DeleteSnapshotOutput, error) {
	f.deletedSnap = aws.ToString(in.SnapshotId)
	if f.delErr != nil {
		return nil, f.delErr
	}
	return &ec2.DeleteSnapshotOutput{}, nil
}

func TestArtifact(t *testing.T) {
	d := &fakeDestroyer{}
	a := &amiArtifact{region: "eu-west-1", amiID: "ami-9", snapshotID: "snap-9", destroyer: d}
	if a.Id() != "eu-west-1:ami-9" {
		t.Fatalf("id: %s", a.Id())
	}
	if a.BuilderId() != builderID || a.Files() != nil {
		t.Fatal("builder id / files")
	}
	if err := a.Destroy(); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if d.deregistered != "ami-9" || d.deletedSnap != "snap-9" {
		t.Fatalf("destroy must deregister ami and delete snapshot: %+v", d)
	}
}

// Destroy on a partial rollback (snapshot uploaded, AMI never registered) must
// delete the orphan snapshot but skip DeregisterImage entirely: there is no AMI.
func TestArtifactDestroyNoAMI(t *testing.T) {
	d := &fakeDestroyer{}
	a := &amiArtifact{region: "eu-west-1", snapshotID: "snap-9", destroyer: d}
	if err := a.Destroy(); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if d.deregCalled {
		t.Fatal("DeregisterImage must not be called when there is no AMI")
	}
	if d.deletedSnap != "snap-9" {
		t.Fatalf("orphan snapshot must be deleted, got %q", d.deletedSnap)
	}
}

// A failed DeregisterImage must not stop the snapshot delete, and both errors
// must surface.
func TestArtifactDestroyBestEffort(t *testing.T) {
	deregErr := errors.New("deregister boom")
	delErr := errors.New("delete boom")
	d := &fakeDestroyer{deregErr: deregErr, delErr: delErr}
	a := &amiArtifact{region: "eu-west-1", amiID: "ami-9", snapshotID: "snap-9", destroyer: d}
	err := a.Destroy()
	if err == nil {
		t.Fatal("destroy must report the failures")
	}
	if d.deletedSnap != "snap-9" {
		t.Fatal("snapshot delete must be attempted even when deregister fails")
	}
	if !errors.Is(err, deregErr) || !errors.Is(err, delErr) {
		t.Fatalf("both errors must surface, got %v", err)
	}
}
