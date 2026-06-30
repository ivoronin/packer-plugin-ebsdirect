// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package ebsdirect

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ebs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type fakeWriter struct {
	mu        sync.Mutex
	started   *ebs.StartSnapshotInput
	putIdx    []int32
	completed *ebs.CompleteSnapshotInput
	putErr    error
}

func (f *fakeWriter) StartSnapshot(_ context.Context, in *ebs.StartSnapshotInput, _ ...func(*ebs.Options)) (*ebs.StartSnapshotOutput, error) {
	f.started = in
	return &ebs.StartSnapshotOutput{SnapshotId: aws.String("snap-test"), BlockSize: aws.Int32(4)}, nil
}

func (f *fakeWriter) PutSnapshotBlock(_ context.Context, in *ebs.PutSnapshotBlockInput, _ ...func(*ebs.Options)) (*ebs.PutSnapshotBlockOutput, error) {
	f.mu.Lock()
	f.putIdx = append(f.putIdx, aws.ToInt32(in.BlockIndex))
	f.mu.Unlock()
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &ebs.PutSnapshotBlockOutput{}, nil
}

func (f *fakeWriter) CompleteSnapshot(_ context.Context, in *ebs.CompleteSnapshotInput, _ ...func(*ebs.Options)) (*ebs.CompleteSnapshotOutput, error) {
	f.completed = in
	return &ebs.CompleteSnapshotOutput{}, nil
}

type fakeWaiter struct{}

func (fakeWaiter) DescribeSnapshots(_ context.Context, _ *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
	return &ec2.DescribeSnapshotsOutput{Snapshots: []ec2types.Snapshot{{State: ec2types.SnapshotStateCompleted}}}, nil
}

func TestUploadSkipsZeroBlocks(t *testing.T) {
	// 8 bytes, fake BlockSize=4 → block0 non-zero, block1 all-zero.
	data := []byte{1, 2, 3, 4, 0, 0, 0, 0}
	w := &fakeWriter{}
	id, err := upload(context.Background(), w, fakeWaiter{}, uploadInput{
		Source:    bytes.NewReader(data),
		SizeBytes: int64(len(data)),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if id != "snap-test" {
		t.Fatalf("want snap-test, got %s", id)
	}
	if len(w.putIdx) != 1 || w.putIdx[0] != 0 {
		t.Fatalf("only block 0 should be put, got %v", w.putIdx)
	}
	if aws.ToInt32(w.completed.ChangedBlocksCount) != 1 {
		t.Fatalf("ChangedBlocksCount want 1, got %d", aws.ToInt32(w.completed.ChangedBlocksCount))
	}
	if aws.ToString(w.completed.Checksum) == "" {
		t.Fatal("aggregate checksum must be set on CompleteSnapshot")
	}
}

func TestUploadConcurrentBlocks(t *testing.T) {
	// 6 blocks of 4 bytes (fake BlockSize=4); blocks 0,1,3,5 non-zero, 2,4 zero.
	data := []byte{
		1, 1, 1, 1,
		2, 2, 2, 2,
		0, 0, 0, 0,
		3, 3, 3, 3,
		0, 0, 0, 0,
		4, 4, 4, 4,
	}
	w := &fakeWriter{}
	if _, err := upload(context.Background(), w, fakeWaiter{}, uploadInput{
		Source:    bytes.NewReader(data),
		SizeBytes: int64(len(data)),
	}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	got := map[int32]bool{}
	for _, idx := range w.putIdx {
		got[idx] = true
	}
	want := []int32{0, 1, 3, 5}
	if len(got) != len(want) {
		t.Fatalf("want puts at %v, got %v", want, w.putIdx)
	}
	for _, idx := range want {
		if !got[idx] {
			t.Fatalf("missing put for block %d, got %v", idx, w.putIdx)
		}
	}
	if n := aws.ToInt32(w.completed.ChangedBlocksCount); n != int32(len(want)) {
		t.Fatalf("ChangedBlocksCount want %d, got %d", len(want), n)
	}
}

func TestUploadPutError(t *testing.T) {
	w := &fakeWriter{putErr: errors.New("boom")}
	_, err := upload(context.Background(), w, fakeWaiter{}, uploadInput{
		Source:    bytes.NewReader([]byte{1, 2, 3, 4}),
		SizeBytes: 4,
	})
	if err == nil {
		t.Fatal("upload must fail when PutSnapshotBlock fails")
	}
	if w.completed != nil {
		t.Fatal("CompleteSnapshot must not run after a put failure")
	}
}

// codedError is a fake AWS API error carrying an ErrorCode, for isNotFound.
type codedError struct{ code string }

func (e codedError) Error() string     { return e.code }
func (e codedError) ErrorCode() string { return e.code }

// scriptedWaiter returns a scripted sequence of DescribeSnapshots results,
// repeating the last entry once the script is exhausted (for the timeout case).
type scriptedWaiter struct {
	states []ec2types.SnapshotState // "" = no snapshot returned this call
	errs   []error
	i      int
}

func (s *scriptedWaiter) DescribeSnapshots(_ context.Context, _ *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
	idx := s.i
	if idx >= len(s.states) {
		idx = len(s.states) - 1
	}
	s.i++
	if err := s.errs[idx]; err != nil {
		return nil, err
	}
	if s.states[idx] == "" {
		return &ec2.DescribeSnapshotsOutput{}, nil
	}
	return &ec2.DescribeSnapshotsOutput{Snapshots: []ec2types.Snapshot{{State: s.states[idx]}}}, nil
}

func TestWaitCompletedRetriesNotFound(t *testing.T) {
	w := &scriptedWaiter{
		states: []ec2types.SnapshotState{"", ec2types.SnapshotStateCompleted},
		errs:   []error{codedError{"InvalidSnapshot.NotFound"}, nil},
	}
	if err := waitCompleted(context.Background(), w, "snap", 0, 3); err != nil {
		t.Fatalf("want nil after NotFound then completed, got %v", err)
	}
}

func TestWaitCompletedErrorState(t *testing.T) {
	w := &scriptedWaiter{
		states: []ec2types.SnapshotState{ec2types.SnapshotStateError},
		errs:   []error{nil},
	}
	if err := waitCompleted(context.Background(), w, "snap", 0, 3); err == nil {
		t.Fatal("want error when snapshot enters error state")
	}
}

func TestWaitCompletedTimeout(t *testing.T) {
	w := &scriptedWaiter{
		states: []ec2types.SnapshotState{ec2types.SnapshotStatePending},
		errs:   []error{nil},
	}
	if err := waitCompleted(context.Background(), w, "snap", 0, 3); err == nil {
		t.Fatal("want timeout error when snapshot never completes")
	}
}

func TestUploadEncryption(t *testing.T) {
	const arn = "arn:aws:kms:eu-central-1:111122223333:key/abc"
	cases := []struct {
		name        string
		encrypt     bool
		kmsKey      string
		wantEncrypt bool
		wantKey     string // "" means expect KmsKeyArn == nil
	}{
		{"encrypt with key", true, arn, true, arn},
		{"encrypt default key", true, "", true, ""},
		{"key ignored without encrypt", false, arn, false, ""},
		{"neither", false, "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeWriter{}
			if _, err := upload(context.Background(), w, fakeWaiter{}, uploadInput{
				Source:    bytes.NewReader([]byte{1, 2, 3, 4}),
				SizeBytes: 4,
				Encrypt:   tc.encrypt,
				KMSKey:    tc.kmsKey,
			}); err != nil {
				t.Fatalf("upload: %v", err)
			}
			if got := aws.ToBool(w.started.Encrypted); got != tc.wantEncrypt {
				t.Fatalf("Encrypted: got %v, want %v", got, tc.wantEncrypt)
			}
			if got := aws.ToString(w.started.KmsKeyArn); got != tc.wantKey {
				t.Fatalf("KmsKeyArn: got %q, want %q", got, tc.wantKey)
			}
		})
	}
}
