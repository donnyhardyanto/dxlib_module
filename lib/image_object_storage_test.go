package lib

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// endlessReader stands in for a chunked upload that never ends.
type endlessReader struct{ read int64 }

func (r *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.read += int64(len(p))
	return len(p), nil
}

func TestReadBodyWithLimit(t *testing.T) {
	var buf bytes.Buffer
	if err := readBodyWithLimit(&buf, strings.NewReader("12345"), 5); err != nil || buf.String() != "12345" {
		t.Fatalf("at the limit: err=%v, body=%q", err, buf.String())
	}

	buf.Reset()
	if err := readBodyWithLimit(&buf, strings.NewReader("123456"), 5); err != errRequestTooLarge {
		t.Fatalf("over the limit: err=%v, want errRequestTooLarge", err)
	}

	buf.Reset()
	r := &endlessReader{}
	if err := readBodyWithLimit(&buf, io.Reader(r), 1<<20); err != errRequestTooLarge {
		t.Fatalf("endless body: err=%v, want errRequestTooLarge", err)
	}
	if r.read > 2<<20 {
		t.Fatalf("read %d bytes of an endless body, limit is 1 MiB", r.read)
	}
}
