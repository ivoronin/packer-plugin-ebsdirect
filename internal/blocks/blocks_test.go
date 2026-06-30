// Copyright (c) Ilya Voronin
// SPDX-License-Identifier: MPL-2.0

package blocks

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
)

const gibTest = 1 << 30

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		size int64
		want error
	}{
		{"empty", 0, ErrEmpty},
		{"negative", -1, ErrEmpty},
		{"not aligned", gibTest + 1, ErrNotAligned},
		{"half gib", gibTest / 2, ErrNotAligned},
		{"exactly 1 gib", gibTest, nil},
		{"exactly 4 gib", 4 * gibTest, nil},
		{"too large", 17 * (1 << 40), ErrTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.size)
			if c.want == nil && err != nil {
				t.Fatalf("want nil, got %v", err)
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestVolumeSizeGiB(t *testing.T) {
	if got := VolumeSizeGiB(3 * gibTest); got != 3 {
		t.Fatalf("want 3, got %d", got)
	}
}

func TestIsZero(t *testing.T) {
	if !IsZero(make([]byte, 16)) {
		t.Fatal("all-zero slice must be zero")
	}
	if IsZero([]byte{0, 0, 1, 0}) {
		t.Fatal("slice with a non-zero byte must not be zero")
	}
}

func TestChecksum(t *testing.T) {
	// SHA-256 of an empty block, base64.
	d, b64 := Checksum(nil)
	if len(d) != 32 {
		t.Fatalf("digest must be 32 bytes, got %d", len(d))
	}
	const wantEmpty = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
	if b64 != wantEmpty {
		t.Fatalf("want %s, got %s", wantEmpty, b64)
	}
}

func TestAggregateChecksum(t *testing.T) {
	// No written blocks → SHA-256 over empty input.
	const wantEmpty = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
	if got := AggregateChecksum([][]byte{nil, nil}); got != wantEmpty {
		t.Fatalf("all-nil: want %s, got %s", wantEmpty, got)
	}
	// Two written blocks: aggregate = base64(sha256(d0 || d1)), nils skipped in index order.
	d0, _ := Checksum([]byte{1})
	d2, _ := Checksum([]byte{2})
	got := AggregateChecksum([][]byte{d0, nil, d2})
	want := independentAggregate(d0, d2)
	if got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func independentAggregate(digests ...[]byte) string {
	h := sha256.New()
	for _, d := range digests {
		h.Write(d)
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
