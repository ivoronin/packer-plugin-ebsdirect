// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ebs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/ivoronin/packer-plugin-ebsdirect/internal/blocks"
)

const gib = 1 << 30

// makeImage builds a 1 GiB sparse image with non-zero data only at EBS block 0
// (a pseudo-MBR pattern) and block 100. Everything else is zero so the uploader
// skips it, and the snapshot listing should contain exactly those two blocks.
func makeImage() (data []byte, nonZero map[int][32]byte) {
	data = make([]byte, gib)
	nonZero = map[int][32]byte{}
	bs := blocks.BlockSize // EBS direct block size: 512 KiB
	for i := 0; i < bs; i++ {
		data[i] = byte(i%251 + 1) // block 0: deterministic non-zero pattern
	}
	mid := 100 * bs
	for i := mid; i < mid+bs; i++ {
		data[i] = byte((i*7)%251 + 1) // block 100: different pattern
	}
	nonZero[0] = sha256.Sum256(data[0:bs])
	nonZero[100] = sha256.Sum256(data[mid : mid+bs])
	return data, nonZero
}

// newE2EDeps gates on PACKER_ACC, loads the default AWS config, and wires the
// EBS/EC2 clients into awsDeps shared by the e2e tests. It also returns the ec2
// and ebs clients for read-side verification.
func newE2EDeps(t *testing.T) (context.Context, awsDeps, *ec2.Client, *ebs.Client) {
	t.Helper()
	if os.Getenv("PACKER_ACC") == "" {
		t.Skip("set PACKER_ACC=1 and AWS credentials to run the e2e tests")
	}
	ctx := context.Background()
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	if cfg.Region == "" {
		t.Fatal("AWS region not configured; set AWS_DEFAULT_REGION or profile region")
	}
	ec2c := ec2.NewFromConfig(cfg)
	ebsc := ebs.NewFromConfig(cfg)
	return ctx, awsDeps{
		writer:    ebsc,
		waiter:    ec2c,
		registrar: ec2c,
		sharer:    ec2c,
		destroyer: ec2c,
		region:    cfg.Region,
	}, ec2c, ebsc
}

// TestE2EUploadReadBack uploads a synthetic 1 GiB sparse image to AWS EBS,
// registers it as an AMI, reads the written blocks back via the EBS direct
// read APIs, and verifies their content matches what was written.
//
// Gate: requires PACKER_ACC=1 and AWS credentials with region configured.
// Teardown: deregisters the AMI and deletes the snapshot via t.Cleanup.
func TestE2EUploadReadBack(t *testing.T) {
	ctx, deps, ec2c, ebsc := newE2EDeps(t)

	// 1 GiB allocation is deferred until after the gate check.
	data, nonZero := makeImage()

	art, err := run(ctx, deps,
		Config{
			AMIName:        "ebsdirect-e2e",
			Architecture:   "x86_64",
			RootDeviceName: "/dev/xvda",
			BootMode:       "legacy-bios",
			Tags:           map[string]string{"ebsdirect-e2e": "1"},
		},
		bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(func() { _ = art.Destroy() })

	// --- Verify AMI state ---

	di, err := ec2c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{art.amiID},
	})
	if err != nil {
		t.Fatalf("describe images: %v", err)
	}
	if len(di.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(di.Images))
	}
	if di.Images[0].State != ec2types.ImageStateAvailable {
		t.Fatalf("ami state: got %q, want %q", di.Images[0].State, ec2types.ImageStateAvailable)
	}

	// Verify the tag round-tripped.
	var tagFound bool
	for _, tag := range di.Images[0].Tags {
		if aws.ToString(tag.Key) == "ebsdirect-e2e" && aws.ToString(tag.Value) == "1" {
			tagFound = true
			break
		}
	}
	if !tagFound {
		t.Fatal("tag ebsdirect-e2e=1 not found on AMI")
	}

	// --- Paginate ListSnapshotBlocks ---

	// A freshly completed snapshot can briefly return ResourceNotFoundException
	// here even though EC2 already reports it completed: the EBS direct read
	// plane lags the control plane. Retry the whole listing until it appears.
	// The read-plane lag after CompleteSnapshot is observed at ~50-60s; budget
	// 30 * 3s = 90s to stay clear of it.
	listedBlocks := make(map[int]string)
	const listAttempts = 30
	for attempt := 1; ; attempt++ {
		listedBlocks = make(map[int]string)
		var nextToken *string
		listErr := func() error {
			for {
				lb, err := ebsc.ListSnapshotBlocks(ctx, &ebs.ListSnapshotBlocksInput{
					SnapshotId: aws.String(art.snapshotID),
					NextToken:  nextToken,
				})
				if err != nil {
					return err
				}
				for _, b := range lb.Blocks {
					listedBlocks[int(aws.ToInt32(b.BlockIndex))] = aws.ToString(b.BlockToken)
				}
				if lb.NextToken == nil || *lb.NextToken == "" {
					return nil
				}
				nextToken = lb.NextToken
			}
		}()
		if listErr == nil {
			break
		}
		if isResourceNotFound(listErr) && attempt < listAttempts {
			t.Logf("ListSnapshotBlocks not ready yet (attempt %d), retrying: %v", attempt, listErr)
			time.Sleep(3 * time.Second)
			continue
		}
		t.Fatalf("list snapshot blocks (attempt %d): %v", attempt, listErr)
	}

	// The snapshot should contain exactly the non-zero blocks we wrote.
	for idx := range listedBlocks {
		if _, expected := nonZero[idx]; !expected {
			t.Fatalf("unexpected non-zero block %d in snapshot", idx)
		}
	}
	for idx := range nonZero {
		if _, present := listedBlocks[idx]; !present {
			t.Fatalf("expected block %d missing from snapshot listing", idx)
		}
	}

	// --- Read each non-zero block back and verify content ---

	for idx, wantHash := range nonZero {
		token := listedBlocks[idx]
		gb, err := ebsc.GetSnapshotBlock(ctx, &ebs.GetSnapshotBlockInput{
			SnapshotId: aws.String(art.snapshotID),
			BlockIndex: aws.Int32(int32(idx)),
			BlockToken: aws.String(token),
		})
		if err != nil {
			t.Fatalf("get block %d: %v", idx, err)
		}
		buf := make([]byte, aws.ToInt32(gb.DataLength))
		// EBS returns exactly DataLength bytes; io.ReadFull returning any error
		// (io.EOF / io.ErrUnexpectedEOF) means a short read, which is a failure.
		n, readErr := io.ReadFull(gb.BlockData, buf)
		gb.BlockData.Close()
		if readErr != nil {
			t.Fatalf("read block %d: short read of %d/%d bytes: %v", idx, n, len(buf), readErr)
		}
		if sha256.Sum256(buf) != wantHash {
			t.Fatalf("block %d data mismatch", idx)
		}
	}
}

// isResourceNotFound reports whether err is an EBS direct ResourceNotFoundException,
// which the read plane returns briefly right after a snapshot completes.
func isResourceNotFound(err error) bool {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "ResourceNotFoundException"
	}
	return false
}

// TestE2EEncryptedSnapshot uploads a small encrypted snapshot with the account
// default EBS key and verifies DescribeSnapshots reports it encrypted.
//
// Gate: requires PACKER_ACC=1 and AWS credentials with region configured.
// Teardown: deregisters the AMI and deletes the snapshot via t.Cleanup.
func TestE2EEncryptedSnapshot(t *testing.T) {
	ctx, deps, ec2c, _ := newE2EDeps(t)

	data, _ := makeImage()

	art, err := run(ctx, deps,
		Config{
			AMIName:        fmt.Sprintf("ebsdirect-e2e-encrypted-%d", time.Now().UnixNano()),
			Architecture:   "x86_64",
			RootDeviceName: "/dev/xvda",
			BootMode:       "legacy-bios",
			Encrypt:        true, // account default EBS key, no KMSKey
			Tags:           map[string]string{"ebsdirect-e2e": "1"},
			SnapshotTags:   map[string]string{"ebsdirect-e2e": "1"},
		},
		bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(func() { _ = art.Destroy() })

	// The snapshot must be encrypted.
	out, err := ec2c.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{art.snapshotID},
	})
	if err != nil {
		t.Fatalf("describe snapshots: %v", err)
	}
	if len(out.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(out.Snapshots))
	}
	if !aws.ToBool(out.Snapshots[0].Encrypted) {
		t.Fatal("snapshot is not encrypted")
	}
	if aws.ToString(out.Snapshots[0].KmsKeyId) == "" {
		t.Fatal("encrypted snapshot has no KmsKeyId")
	}

	// The registered AMI must inherit the snapshot's encryption on its root
	// block device. registrar.go relies on this inheritance and does not set
	// encryption explicitly, so the e2e proves it end to end.
	di, err := ec2c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{art.amiID},
	})
	if err != nil {
		t.Fatalf("describe images: %v", err)
	}
	if len(di.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(di.Images))
	}
	var rootEncrypted bool
	for _, bdm := range di.Images[0].BlockDeviceMappings {
		if bdm.Ebs != nil && aws.ToBool(bdm.Ebs.Encrypted) {
			rootEncrypted = true
		}
	}
	if !rootEncrypted {
		t.Fatal("registered AMI root block device is not encrypted")
	}
}

// TestE2EIMDSSupport registers an AMI with imds_support=v2.0 and verifies
// DescribeImages reports it on the AMI.
//
// Gate: requires PACKER_ACC=1 and AWS credentials with region configured.
// Teardown: deregisters the AMI and deletes the snapshot via t.Cleanup.
func TestE2EIMDSSupport(t *testing.T) {
	ctx, deps, ec2c, _ := newE2EDeps(t)

	data, _ := makeImage()

	art, err := run(ctx, deps,
		Config{
			AMIName:        fmt.Sprintf("ebsdirect-e2e-imds-%d", time.Now().UnixNano()),
			Architecture:   "x86_64",
			RootDeviceName: "/dev/xvda",
			BootMode:       "legacy-bios",
			IMDSSupport:    "v2.0",
			Tags:           map[string]string{"ebsdirect-e2e": "1"},
			SnapshotTags:   map[string]string{"ebsdirect-e2e": "1"},
		},
		bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(func() { _ = art.Destroy() })

	di, err := ec2c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{art.amiID},
	})
	if err != nil {
		t.Fatalf("describe images: %v", err)
	}
	if len(di.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(di.Images))
	}
	if di.Images[0].ImdsSupport != ec2types.ImdsSupportValuesV20 {
		t.Fatalf("ImdsSupport: got %q, want v2.0", di.Images[0].ImdsSupport)
	}
}

// TestE2EAMISharing registers an AMI, shares its launch permission with a
// placeholder account id, and verifies DescribeImageAttribute reports it.
//
// Gate: requires PACKER_ACC=1 and AWS credentials with region configured.
// Teardown: deregisters the AMI (which removes the launch permission) and
// deletes the snapshot via t.Cleanup.
func TestE2EAMISharing(t *testing.T) {
	ctx, deps, ec2c, _ := newE2EDeps(t)

	data, _ := makeImage()

	// AWS does not validate account existence when adding launch permissions,
	// so a placeholder id is a safe, verifiable share on a synthetic AMI.
	const shareAcct = "123456789012"

	art, err := run(ctx, deps,
		Config{
			AMIName:        fmt.Sprintf("ebsdirect-e2e-share-%d", time.Now().UnixNano()),
			Architecture:   "x86_64",
			RootDeviceName: "/dev/xvda",
			BootMode:       "legacy-bios",
			AMIUsers:       []string{shareAcct},
			Tags:           map[string]string{"ebsdirect-e2e": "1"},
			SnapshotTags:   map[string]string{"ebsdirect-e2e": "1"},
		},
		bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(func() { _ = art.Destroy() })

	da, err := ec2c.DescribeImageAttribute(ctx, &ec2.DescribeImageAttributeInput{
		ImageId:   aws.String(art.amiID),
		Attribute: ec2types.ImageAttributeNameLaunchPermission,
	})
	if err != nil {
		t.Fatalf("describe image attribute: %v", err)
	}
	var found bool
	for _, p := range da.LaunchPermissions {
		if aws.ToString(p.UserId) == shareAcct {
			found = true
		}
	}
	if !found {
		t.Fatalf("launch permission for %s not found: %+v", shareAcct, da.LaunchPermissions)
	}
}
