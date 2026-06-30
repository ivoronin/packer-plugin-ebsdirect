// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/ebs"
	ebstypes "github.com/aws/aws-sdk-go-v2/service/ebs/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/sync/errgroup"

	"github.com/ivoronin/packer-plugin-ebsdirect/internal/blocks"
)

const (
	uploadWorkers          = 64
	snapshotTimeoutMinutes = 60 // StartSnapshot auto-cancel window
	pollInterval           = 5 * time.Second
	pollTimeout            = 10 * time.Minute
	pollMaxAttempts        = int(pollTimeout / pollInterval)
)

// uploadInput is the request to turn a raw image into a completed snapshot.
type uploadInput struct {
	Source      io.ReaderAt
	SizeBytes   int64
	Description string
	Tags        map[string]string
	Encrypt     bool
	KMSKey      string
}

// upload writes src into a new EBS snapshot via the EBS direct APIs and waits
// until it is completed, returning the snapshot id. The image must already be
// GiB-aligned (the caller validates via blocks.Validate).
func upload(ctx context.Context, w snapshotWriter, waiter snapshotWaiter, in uploadInput) (string, error) {
	start, err := w.StartSnapshot(ctx, &ebs.StartSnapshotInput{
		VolumeSize:  aws.Int64(blocks.VolumeSizeGiB(in.SizeBytes)),
		Timeout:     aws.Int32(snapshotTimeoutMinutes),
		Description: optionalString(in.Description),
		Tags:        ebsTags(in.Tags),
		Encrypted:   aws.Bool(in.Encrypt),
		KmsKeyArn:   kmsKeyArn(in.Encrypt, in.KMSKey),
	})
	if err != nil {
		return "", fmt.Errorf("start snapshot: %w", err)
	}
	snapshotID := aws.ToString(start.SnapshotId)
	blockSize := int64(aws.ToInt32(start.BlockSize))

	count := int(in.SizeBytes / blockSize)
	digests := make([][]byte, count)

	if err := putBlocks(ctx, w, snapshotID, blockSize, in.Source, count, digests); err != nil {
		return "", fmt.Errorf("put blocks into %s: %w", snapshotID, err)
	}

	if _, err := w.CompleteSnapshot(ctx, &ebs.CompleteSnapshotInput{
		SnapshotId:                aws.String(snapshotID),
		ChangedBlocksCount:        aws.Int32(changedBlocks(digests)),
		Checksum:                  aws.String(blocks.AggregateChecksum(digests)),
		ChecksumAlgorithm:         ebstypes.ChecksumAlgorithmChecksumAlgorithmSha256,
		ChecksumAggregationMethod: ebstypes.ChecksumAggregationMethodChecksumAggregationLinear,
	}); err != nil {
		return "", fmt.Errorf("complete snapshot %s: %w", snapshotID, err)
	}

	if err := waitCompleted(ctx, waiter, snapshotID, pollInterval, pollMaxAttempts); err != nil {
		return "", err
	}
	return snapshotID, nil
}

// putBlocks uploads every non-zero block of src concurrently, recording each
// written block's raw SHA-256 digest at its index in digests; skipped all-zero
// blocks stay nil. It stops at the first error and returns it. Block buffers
// are pooled so peak memory stays bounded regardless of image size.
func putBlocks(ctx context.Context, w snapshotWriter, snapshotID string, blockSize int64,
	src io.ReaderAt, count int, digests [][]byte,
) error {
	bufPool := sync.Pool{New: func() any { b := make([]byte, blockSize); return &b }}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(uploadWorkers)
	for i := 0; i < count; i++ {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			bufp, _ := bufPool.Get().(*[]byte)
			defer bufPool.Put(bufp)
			buf := *bufp

			if n, err := src.ReadAt(buf, int64(i)*blockSize); n != len(buf) {
				return fmt.Errorf("read block %d: short read %d/%d: %w", i, n, len(buf), err)
			}
			if blocks.IsZero(buf) {
				return nil
			}
			digest, checksum := blocks.Checksum(buf)
			digests[i] = digest

			if _, err := w.PutSnapshotBlock(gctx, &ebs.PutSnapshotBlockInput{
				SnapshotId:        aws.String(snapshotID),
				BlockIndex:        aws.Int32(int32(i)), //nolint:gosec // G115: i < count = size/blockSize <= 16 TiB/512 KiB < int32 max
				BlockData:         bytes.NewReader(buf),
				DataLength:        aws.Int32(int32(len(buf))), //nolint:gosec // G115: len(buf) == blockSize (512 KiB) < int32 max
				Checksum:          aws.String(checksum),
				ChecksumAlgorithm: ebstypes.ChecksumAlgorithmChecksumAlgorithmSha256,
			}); err != nil {
				return fmt.Errorf("put block %d: %w", i, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// changedBlocks counts the blocks actually written (non-nil digests).
func changedBlocks(digests [][]byte) int32 {
	var n int32
	for _, d := range digests {
		if d != nil {
			n++
		}
	}
	return n
}

func waitCompleted(ctx context.Context, waiter snapshotWaiter, snapshotID string, interval time.Duration, maxAttempts int) error {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		out, err := waiter.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
			SnapshotIds: []string{snapshotID},
		})
		switch {
		case err != nil && isNotFound(err):
			// not visible yet right after CompleteSnapshot; keep polling
		case err != nil:
			return fmt.Errorf("describe snapshot %s: %w", snapshotID, err)
		case len(out.Snapshots) > 0:
			switch out.Snapshots[0].State {
			case ec2types.SnapshotStateCompleted:
				return nil
			case ec2types.SnapshotStateError:
				return fmt.Errorf("snapshot %s entered error state", snapshotID)
			}
		}
		if err := sleep(ctx, interval); err != nil {
			return err
		}
	}
	return fmt.Errorf("snapshot %s not completed after %d attempts", snapshotID, maxAttempts)
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

// kmsKeyArn returns the KMS key ARN only when encryption is on; an empty key
// under encryption falls back to the default EBS key, and a key without
// encryption is ignored (parity with amazon-import).
func kmsKeyArn(encrypt bool, key string) *string {
	if !encrypt {
		return nil
	}
	return optionalString(key)
}

func ebsTags(m map[string]string) []ebstypes.Tag {
	if len(m) == 0 {
		return nil
	}
	tags := make([]ebstypes.Tag, 0, len(m))
	for k, v := range m {
		tags = append(tags, ebstypes.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return tags
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func isNotFound(err error) bool {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "InvalidSnapshot.NotFound"
	}
	return false
}

// tunedHTTPClient builds an HTTP client whose connection pool matches the
// upload concurrency, so the workers do not serialize on connection setup.
func tunedHTTPClient() *awshttp.BuildableClient {
	return awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
		t.MaxConnsPerHost = uploadWorkers
		t.MaxIdleConnsPerHost = uploadWorkers
		t.MaxIdleConns = uploadWorkers
	})
}
