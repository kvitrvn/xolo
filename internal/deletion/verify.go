package deletion

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/pkg/errors"
)

// maxLineSize bounds the memory a single line may take.
const maxLineSize = 16 << 20

// ErrInvalidExport refuses an incomplete, altered or incompatible export.
var ErrInvalidExport = errors.New("invalid deletion export")

// Summary describes a verified export.
type Summary struct {
	Deletion Deletion       `json:"deletion"`
	Count    int            `json:"count"`
	Tables   map[string]int `json:"tables"`
	SHA256   string         `json:"sha256"`
}

func invalid(line int, format string, args ...any) error {
	return errors.Wrapf(ErrInvalidExport, "line %d: %s", line, fmt.Sprintf(format, args...))
}

// Verify reads a whole export and checks its integrity and its completeness,
// holding one line at a time.
func Verify(r io.Reader) (Summary, error) {
	summary := Summary{Tables: map[string]int{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	scanner.Split(scanLinesKeepingNewline)

	digest := sha256.New()
	var trailer *Trailer
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Bytes()
		if raw[len(raw)-1] != '\n' {
			return summary, invalid(line, "truncated line")
		}
		if trailer != nil {
			return summary, invalid(line, "data after the trailer")
		}
		body := raw[:len(raw)-1]
		if line == 1 {
			var header Header
			if err := decodeStrict(body, &header); err != nil {
				return summary, invalid(line, "header: %v", err)
			}
			if header.Format != Format || header.Source == "" || header.Deletion.ResourceID == "" {
				return summary, invalid(line, "incompatible header")
			}
			summary.Deletion = header.Deletion
			digest.Write(raw)
			continue
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(body, &probe); err != nil {
			return summary, invalid(line, "%v", err)
		}
		if _, ok := probe["sha256"]; ok {
			trailer = &Trailer{}
			if err := decodeStrict(body, trailer); err != nil {
				return summary, invalid(line, "trailer: %v", err)
			}
			continue
		}
		var record Record
		if err := decodeStrict(body, &record); err != nil || record.Table == "" || !bytes.HasPrefix(record.Row, []byte("{")) {
			return summary, invalid(line, "invalid record")
		}
		summary.Count++
		summary.Tables[record.Table]++
		digest.Write(raw)
	}
	if err := scanner.Err(); err != nil {
		return summary, errors.Wrap(ErrInvalidExport, err.Error())
	}
	if line == 0 {
		return summary, errors.Wrap(ErrInvalidExport, "empty export")
	}
	if trailer == nil || !trailer.Complete {
		return summary, errors.Wrap(ErrInvalidExport, "incomplete export: missing trailer")
	}
	if trailer.Count != summary.Count {
		return summary, errors.Wrapf(ErrInvalidExport, "trailer counts %d records, export holds %d", trailer.Count, summary.Count)
	}
	want := hex.EncodeToString(digest.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(trailer.SHA256), []byte(want)) != 1 {
		return summary, errors.Wrap(ErrInvalidExport, "checksum mismatch")
	}
	summary.SHA256 = want
	return summary, nil
}

// scanLinesKeepingNewline splits on '\n' and keeps it, so the digest covers
// the exact bytes; a last line without newline is returned as is and refused.
func scanLinesKeepingNewline(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func decodeStrict(raw []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing data")
	}
	return nil
}
