// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

// Package blocks holds the pure, AWS-free rules for turning a raw disk image
// into EBS direct API blocks: sizing, zero detection, and checksums.
package blocks

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// BlockSize is the fixed EBS direct API block size: 512 KiB.
const BlockSize = 512 * 1024

const (
	gib           = 1 << 30
	maxImageBytes = 16 * (1 << 40) // gp3 volume ceiling: 16 TiB
)

var (
	ErrEmpty      = errors.New("image is empty")
	ErrNotAligned = errors.New("image size is not a whole multiple of 1 GiB")
	ErrTooLarge   = errors.New("image exceeds the 16 TiB gp3 ceiling")
)

// Validate reports whether an image of sizeBytes can become a snapshot:
// non-empty, a whole multiple of 1 GiB, within the 16 TiB gp3 ceiling.
func Validate(sizeBytes int64) error {
	switch {
	case sizeBytes <= 0:
		return ErrEmpty
	case sizeBytes%gib != 0:
		return fmt.Errorf("%w: %d bytes; round up to %d GiB",
			ErrNotAligned, sizeBytes, sizeBytes/gib+1)
	case sizeBytes > maxImageBytes:
		return fmt.Errorf("%w: %d bytes", ErrTooLarge, sizeBytes)
	default:
		return nil
	}
}

// VolumeSizeGiB returns the snapshot volume size in whole GiB.
// Precondition: Validate(sizeBytes) == nil.
func VolumeSizeGiB(sizeBytes int64) int64 { return sizeBytes / gib }

// IsZero reports whether block is entirely zero bytes.
func IsZero(block []byte) bool {
	for _, b := range block {
		if b != 0 {
			return false
		}
	}
	return true
}

// Checksum returns the raw SHA-256 digest of block and its base64 encoding
// (the form PutSnapshotBlock expects in the x-amz-Checksum header).
func Checksum(block []byte) (digest []byte, b64 string) {
	sum := sha256.Sum256(block)
	return sum[:], base64.StdEncoding.EncodeToString(sum[:])
}

// AggregateChecksum computes the LINEAR aggregated checksum for CompleteSnapshot
// from per-block raw digests indexed by block index. Nil entries (skipped
// all-zero blocks) are omitted; the rest are concatenated in ascending index
// order, SHA-256'd, and base64-encoded.
func AggregateChecksum(blockDigests [][]byte) string {
	h := sha256.New()
	for _, d := range blockDigests {
		if d != nil {
			h.Write(d)
		}
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
