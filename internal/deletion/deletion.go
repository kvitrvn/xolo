// Package deletion defines the export of the scope of a deleted resource:
// every row the purge removes, read on one snapshot, secrets left out. Its
// digest confirms the purge.
//
// The format is NDJSON, written and verified as a stream:
//
//	{"format":"xolo-deletion/1","source":…,"deletion":{…}}
//	{"table":…,"row":{…}}   (one per row)
//	{"count":…,"complete":true,"sha256":…}
//
// The SHA-256 covers the exact bytes of every line before the last one,
// newlines included. The scope is frozen, and nothing in the export depends
// on the time it is made: two exports of the same deletion give the same
// bytes, unless a purge removed rows of the scope in between. The client
// confirms the purge with the digest of the trailer, which the server checks
// against a new export.
package deletion

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"time"

	"github.com/pkg/errors"
	"github.com/xolo-gateway/xolo/internal/core/model"
)

// Format identifies the format.
const Format = "xolo-deletion/1"

// Reader reads the scope of a deletion on one snapshot.
type Reader interface {
	ReadDeletionScope(ctx context.Context, scope model.CommonScope, key string, start func(source string, deletion model.Deletion) error, row func(table string, row json.RawMessage) error) error
}

// Deletion is the part of a deletion the export holds: what does not change
// until its purge.
type Deletion struct {
	Family     string    `json:"resource_type"`
	TenantID   string    `json:"tenant_id"`
	ResourceID string    `json:"resource_id"`
	DeletedAt  time.Time `json:"deleted_at"`
	PurgeAfter time.Time `json:"purge_after"`
	ETag       string    `json:"etag"`
}

// Header is the first line of an export.
type Header struct {
	Format   string   `json:"format"`
	Source   string   `json:"source"`
	Deletion Deletion `json:"deletion"`
}

// Record is one row of a table of the scope.
type Record struct {
	Table string          `json:"table"`
	Row   json.RawMessage `json:"row"`
}

// Trailer is the last line of a complete export.
type Trailer struct {
	Count    int    `json:"count"`
	Complete bool   `json:"complete"`
	SHA256   string `json:"sha256"`
}

// Export writes the export of the deletion of a resource to w, and returns
// its trailer. On failure the output lacks its trailer, so Verify rejects it.
func Export(ctx context.Context, reader Reader, scope model.CommonScope, key string, w io.Writer) (Trailer, error) {
	out := bufio.NewWriter(w)
	digest := sha256.New()
	count := 0
	err := reader.ReadDeletionScope(ctx, scope, key,
		func(source string, d model.Deletion) error {
			return writeLine(out, digest, Header{Format: Format, Source: source, Deletion: Deletion{
				Family: d.Family, TenantID: d.TenantID, ResourceID: d.ResourceID,
				DeletedAt: d.DeletedAt.UTC(), PurgeAfter: d.PurgeAfter.UTC(), ETag: d.ETag,
			}})
		},
		func(table string, row json.RawMessage) error {
			count++
			return writeLine(out, digest, Record{Table: table, Row: row})
		},
	)
	if err != nil {
		return Trailer{}, err
	}
	trailer := Trailer{Count: count, Complete: true, SHA256: hex.EncodeToString(digest.Sum(nil))}
	if err := writeLine(out, nil, trailer); err != nil {
		return Trailer{}, err
	}
	return trailer, errors.WithStack(out.Flush())
}

// Digest computes the digest of the current export of the deletion of a
// resource, holding one row at a time.
func Digest(ctx context.Context, reader Reader, scope model.CommonScope, key string) (string, error) {
	trailer, err := Export(ctx, reader, scope, key, io.Discard)
	return trailer.SHA256, err
}

// ValidDigest reports whether digest is a SHA-256 written as the trailer
// writes it: 64 lowercase hexadecimal characters.
func ValidDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	for _, c := range digest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func writeLine(w io.Writer, digest hash.Hash, value any) error {
	line, err := json.Marshal(value)
	if err != nil {
		return errors.WithStack(err)
	}
	line = append(line, '\n')
	if digest != nil {
		digest.Write(line)
	}
	_, err = w.Write(line)
	return errors.WithStack(err)
}
