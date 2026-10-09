package deletion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xolo-gateway/xolo/internal/core/model"
)

// fakeScope serves fixed rows.
type fakeScope struct {
	rows []Record
	fail error
}

func (f fakeScope) ReadDeletionScope(ctx context.Context, scope model.CommonScope, key string, start func(string, model.Deletion) error, row func(string, json.RawMessage) error) error {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if err := start("urn:uuid:feed", model.Deletion{Family: model.FamilyOrganization, TenantID: "11111111-1111-4111-8111-111111111111",
		ResourceID: key, DeletedAt: at, PurgeAfter: at.Add(time.Hour), ETag: `W/"7"`}); err != nil {
		return err
	}
	for _, r := range f.rows {
		if err := row(r.Table, r.Row); err != nil {
			return err
		}
	}
	return f.fail
}

const orgID = "22222222-2222-4222-8222-222222222222"

var orgScope = model.CommonScope{Family: model.FamilyOrganization, TenantID: "11111111-1111-4111-8111-111111111111"}

func validScope() fakeScope {
	return fakeScope{rows: []Record{
		{Table: "organizations", Row: json.RawMessage(`{"id":"` + orgID + `","name":"Org"}`)},
		{Table: "memberships", Row: json.RawMessage(`{"id":"m1","org_id":"` + orgID + `"}`)},
		{Table: "memberships", Row: json.RawMessage(`{"id":"m2","org_id":"` + orgID + `"}`)},
	}}
}

func export(t *testing.T, scope fakeScope) []byte {
	t.Helper()
	var out bytes.Buffer
	_, err := Export(t.Context(), scope, orgScope, orgID, &out)
	require.NoError(t, err)
	return out.Bytes()
}

// framed writes raw lines with a trailer whose checksum covers them, so that
// only the checks of the content can refuse them. trailer may alter it.
func framed(lines []string, trailer func(*Trailer)) string {
	var out strings.Builder
	digest := sha256.New()
	for _, line := range lines {
		digest.Write([]byte(line + "\n"))
		out.WriteString(line + "\n")
	}
	end := Trailer{Count: len(lines) - 1, Complete: true, SHA256: hex.EncodeToString(digest.Sum(nil))}
	if trailer != nil {
		trailer(&end)
	}
	encoded, _ := json.Marshal(end)
	out.Write(append(encoded, '\n'))
	return out.String()
}

func TestExportRoundTrip(t *testing.T) {
	raw := export(t, validScope())
	require.Equal(t, raw, export(t, validScope()), "the same scope gives the same bytes")
	summary, err := Verify(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, orgID, summary.Deletion.ResourceID)
	require.Equal(t, `W/"7"`, summary.Deletion.ETag)
	require.Equal(t, 3, summary.Count)
	require.Equal(t, map[string]int{"organizations": 1, "memberships": 2}, summary.Tables)
	lines := bytes.SplitAfter(raw, []byte("\n"))
	sum := sha256.Sum256(bytes.Join(lines[:len(lines)-2], nil))
	require.Equal(t, hex.EncodeToString(sum[:]), summary.SHA256, "the digest covers every line before the trailer")
	digest, err := Digest(t.Context(), validScope(), orgScope, orgID)
	require.NoError(t, err)
	require.Equal(t, summary.SHA256, digest)
	require.True(t, ValidDigest(digest))
}

func TestExportFailureHasNoTrailer(t *testing.T) {
	scope := validScope()
	scope.fail = errors.New("connection lost")
	var out bytes.Buffer
	_, err := Export(t.Context(), scope, orgScope, orgID, &out)
	require.Error(t, err)
	_, err = Verify(bytes.NewReader(out.Bytes()))
	require.ErrorIs(t, err, ErrInvalidExport)
}

func TestVerifyRejects(t *testing.T) {
	valid := string(export(t, validScope()))
	lines := strings.SplitAfter(valid, "\n")
	lines = lines[:len(lines)-1] // the empty string after the last newline
	header := strings.TrimSuffix(lines[0], "\n")
	record := strings.TrimSuffix(lines[1], "\n")
	_, err := Verify(strings.NewReader(framed([]string{header, record}, nil)))
	require.NoError(t, err, "an untouched framed export is valid: each case below fails on its own check")
	without := func(i int) string {
		return strings.Join(append(append([]string{}, lines[:i]...), lines[i+1:]...), "")
	}
	cases := map[string]string{
		"empty":                "",
		"altered byte":         strings.Replace(valid, `"Org"`, `"Orf"`, 1),
		"truncated":            valid[:len(valid)-10],
		"no final newline":     strings.TrimSuffix(valid, "\n"),
		"missing trailer":      without(len(lines) - 1),
		"missing record":       without(2),
		"data after":           valid + lines[1],
		"wrong count":          strings.Replace(valid, `"count":3`, `"count":4`, 1),
		"incompatible":         framed([]string{strings.Replace(header, Format, "xolo-deletion/2", 1), record}, nil),
		"no source":            framed([]string{strings.Replace(header, `"source":"urn:uuid:feed"`, `"source":""`, 1), record}, nil),
		"no resource":          framed([]string{strings.Replace(header, `"resource_id":"`+orgID+`"`, `"resource_id":""`, 1), record}, nil),
		"unknown header field": framed([]string{strings.Replace(header, `"source":`, `"extra":1,"source":`, 1), record}, nil),
		"header not an object": framed([]string{`[]`, record}, nil),
		"incomplete":           framed([]string{header, record}, func(t *Trailer) { t.Complete = false }),
		"count disagrees":      framed([]string{header, record}, func(t *Trailer) { t.Count = 2 }),
		"row not an object":    framed([]string{header, `{"table":"organizations","row":42}`}, nil),
		"row is null":          framed([]string{header, `{"table":"organizations","row":null}`}, nil),
		"no table":             framed([]string{header, `{"table":"","row":{}}`}, nil),
		"unknown record field": framed([]string{header, `{"table":"organizations","row":{},"extra":1}`}, nil),
		"line too long": framed([]string{header, `{"table":"organizations","row":{"name":"` +
			strings.Repeat("x", maxLineSize) + `"}}`}, nil),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Verify(strings.NewReader(raw))
			require.ErrorIs(t, err, ErrInvalidExport)
		})
	}
}

func TestValidDigest(t *testing.T) {
	sum := sha256.Sum256(nil)
	require.True(t, ValidDigest(hex.EncodeToString(sum[:])))
	for _, invalid := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.ToUpper(hex.EncodeToString(sum[:])), strings.Repeat("g", 64)} {
		require.False(t, ValidDigest(invalid), invalid)
	}
}
