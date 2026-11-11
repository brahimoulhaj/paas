package executor

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func frame(stream byte, payload string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

func TestDemuxLogs(t *testing.T) {
	var in bytes.Buffer
	// A line split across two frames, interleaved with stderr, and a last line with no newline.
	in.Write(frame(1, "2026-11-10T10:00:00.000000001Z hel"))
	in.Write(frame(2, "2026-11-10T10:00:00.5Z oops\n"))
	in.Write(frame(1, "lo\n2026-11-10T10:00:01Z \n"))
	in.Write(frame(1, "2026-11-10T10:00:02Z tail"))

	var got []LogLine
	if err := demuxLogs(&in, func(l LogLine) { got = append(got, l) }); err != nil {
		t.Fatal(err)
	}
	want := []struct{ stream, text string }{
		{"stderr", "oops"},
		{"stdout", "hello"},
		{"stdout", ""},
		{"stdout", "tail"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Stream != w.stream || got[i].Text != w.text {
			t.Errorf("line %d = %q %q, want %q %q", i, got[i].Stream, got[i].Text, w.stream, w.text)
		}
		if got[i].Time.IsZero() {
			t.Errorf("line %d: timestamp not parsed", i)
		}
	}
}

func TestDemuxLogsTruncatedStream(t *testing.T) {
	data := frame(1, "2026-11-10T10:00:00Z complete\n")
	data = append(data, frame(1, "2026-11-10T10:00:01Z cut off here")[:20]...)
	var got []LogLine
	if err := demuxLogs(bytes.NewReader(data), func(l LogLine) { got = append(got, l) }); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Text != "complete" {
		t.Fatalf("got %+v", got)
	}
}
