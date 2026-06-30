// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"bytes"
	"context"
	"errors"
	"testing"
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
