// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestRunCleansSnapshotOnRegisterFailure(t *testing.T) {
	d := &fakeDestroyer{}
	deps := awsDeps{
		writer:    &fakeWriter{},
		waiter:    fakeWaiter{},
		registrar: &fakeRegistrar{err: errors.New("boom")},
		destroyer: d,
		region:    "eu-west-1",
	}
	_, err := run(context.Background(), deps, Config{AMIName: "img"}, bytes.NewReader([]byte{1, 2, 3, 4}), 4)
	if err == nil {
		t.Fatal("run must fail when RegisterImage fails")
	}
	if d.deletedSnap != "snap-test" {
		t.Fatalf("orphan snapshot must be deleted, got %q", d.deletedSnap)
	}
}

func TestRunHappyPath(t *testing.T) {
	r := &fakeRegistrar{}
	d := &fakeDestroyer{}
	deps := awsDeps{writer: &fakeWriter{}, waiter: fakeWaiter{}, registrar: r, destroyer: d, region: "eu-west-1"}
	art, err := run(context.Background(), deps,
		Config{AMIName: "img", Architecture: "x86_64", RootDeviceName: "/dev/xvda", BootMode: "legacy-bios"},
		bytes.NewReader([]byte{1, 2, 3, 4}), 4)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if art.Id() != "eu-west-1:ami-test" {
		t.Fatalf("artifact id: %s", art.Id())
	}
}

type fakeArtifact struct{ files []string }

func (a fakeArtifact) BuilderId() string        { return "test" }
func (a fakeArtifact) Files() []string          { return a.files }
func (a fakeArtifact) Id() string               { return "x" }
func (a fakeArtifact) String() string           { return "x" }
func (a fakeArtifact) State(string) interface{} { return nil }
func (a fakeArtifact) Destroy() error           { return nil }

func TestPickSourceFile(t *testing.T) {
	if _, err := pickSourceFile(fakeArtifact{files: []string{"a.qcow2"}}); err == nil {
		t.Fatal("must fail when no .raw present")
	}
	got, err := pickSourceFile(fakeArtifact{files: []string{"a.qcow2", "disk.raw"}})
	if err != nil || got != "disk.raw" {
		t.Fatalf("want disk.raw, got %q err %v", got, err)
	}
	if _, err := pickSourceFile(fakeArtifact{files: []string{"a.raw", "b.raw"}}); err == nil {
		t.Fatal("must fail on ambiguous match")
	}
}

func TestRequireRegion(t *testing.T) {
	if err := requireRegion(""); err == nil {
		t.Fatal("empty region must error")
	}
	if err := requireRegion("eu-central-1"); err != nil {
		t.Fatalf("resolved region must be ok, got %v", err)
	}
}

func TestRunCleansAMIOnShareFailure(t *testing.T) {
	d := &fakeDestroyer{}
	deps := awsDeps{
		writer:    &fakeWriter{},
		waiter:    fakeWaiter{},
		registrar: &fakeRegistrar{},
		sharer:    &fakeSharer{err: errors.New("boom")},
		destroyer: d,
		region:    "eu-west-1",
	}
	_, err := run(context.Background(), deps,
		Config{AMIName: "img", AMIUsers: []string{"111111111111"}},
		bytes.NewReader([]byte{1, 2, 3, 4}), 4)
	if err == nil {
		t.Fatal("run must fail when sharing fails")
	}
	if d.deregistered != "ami-test" || d.deletedSnap != "snap-test" {
		t.Fatalf("failed share must tear down AMI and snapshot, got %+v", d)
	}
}

// When the share cleanup itself fails, run must join the share error and the
// teardown error rather than dropping either.
func TestRunShareFailureJoinsCleanupError(t *testing.T) {
	shareErr := errors.New("share boom")
	d := &fakeDestroyer{deregErr: errors.New("deregister boom")}
	deps := awsDeps{
		writer:    &fakeWriter{},
		waiter:    fakeWaiter{},
		registrar: &fakeRegistrar{},
		sharer:    &fakeSharer{err: shareErr},
		destroyer: d,
		region:    "eu-west-1",
	}
	_, err := run(context.Background(), deps,
		Config{AMIName: "img", AMIUsers: []string{"111111111111"}},
		bytes.NewReader([]byte{1, 2, 3, 4}), 4)
	if err == nil {
		t.Fatal("run must fail when sharing fails")
	}
	if !errors.Is(err, shareErr) || !errors.Is(err, d.deregErr) {
		t.Fatalf("run must join the share error and the cleanup error, got %v", err)
	}
}

// run must map every Config sharing field onto the ModifyImageAttribute call,
// guarding against a cfg -> shareInput copy-paste swap.
func TestRunSharesAllPrincipals(t *testing.T) {
	s := &fakeSharer{}
	deps := awsDeps{
		writer:    &fakeWriter{},
		waiter:    fakeWaiter{},
		registrar: &fakeRegistrar{},
		sharer:    s,
		destroyer: &fakeDestroyer{},
		region:    "eu-west-1",
	}
	_, err := run(context.Background(), deps,
		Config{
			AMIName:    "img",
			AMIUsers:   []string{"111111111111"},
			AMIGroups:  []string{"all"},
			AMIOrgArns: []string{"arn:aws:organizations::111111111111:organization/o-abc"},
			AMIOuArns:  []string{"arn:aws:organizations::111111111111:ou/o-abc/ou-xyz"},
		},
		bytes.NewReader([]byte{1, 2, 3, 4}), 4)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if s.got == nil {
		t.Fatal("ModifyImageAttribute was not called")
	}
	add := s.got.LaunchPermission.Add
	if len(add) != 4 {
		t.Fatalf("want 4 launch permissions, got %d", len(add))
	}
	var user, group, org, ou bool
	for _, p := range add {
		switch {
		case aws.ToString(p.UserId) == "111111111111":
			user = true
		case p.Group == ec2types.PermissionGroupAll:
			group = true
		case aws.ToString(p.OrganizationArn) == "arn:aws:organizations::111111111111:organization/o-abc":
			org = true
		case aws.ToString(p.OrganizationalUnitArn) == "arn:aws:organizations::111111111111:ou/o-abc/ou-xyz":
			ou = true
		}
	}
	if !user || !group || !org || !ou {
		t.Fatalf("cfg -> shareInput mapping dropped a principal: user=%v group=%v org=%v ou=%v", user, group, org, ou)
	}
}
