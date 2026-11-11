package executor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"time"
)

// maxLogLine caps one line held in memory; longer lines are split.
const maxLogLine = 64 * 1024

// demuxLogs reads Docker's multiplexed log stream (8-byte header: stream type, 3 zero
// bytes, big-endian payload length) and calls fn once per complete line. Frames do not
// align with lines, so each stream keeps its own partial-line buffer. Lines are expected
// to start with an RFC 3339 timestamp (the Timestamps log option).
func demuxLogs(r io.Reader, fn func(LogLine)) error {
	var header [8]byte
	var bufs [3]bytes.Buffer // indexed by stream type: 1 stdout, 2 stderr (0 stdin is unused)
	names := [3]string{"stdout", "stdout", "stderr"}

	emit := func(stream int, raw []byte) {
		fn(parseLogLine(names[stream], strings.TrimRight(string(raw), "\r")))
	}
	flush := func() {
		for i := range bufs {
			if bufs[i].Len() > 0 {
				emit(i, bufs[i].Bytes())
				bufs[i].Reset()
			}
		}
	}

	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			flush()
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		stream := int(header[0])
		if stream > 2 {
			stream = 1
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		buf := &bufs[stream]
		if _, err := io.CopyN(buf, r, size); err != nil {
			flush()
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		for {
			data := buf.Bytes()
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				if buf.Len() > maxLogLine {
					emit(stream, data)
					buf.Reset()
				}
				break
			}
			emit(stream, data[:i])
			buf.Next(i + 1)
		}
	}
}

func parseLogLine(stream, s string) LogLine {
	line := LogLine{Stream: stream, Text: s}
	if ts, rest, ok := strings.Cut(s, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			line.Time, line.Text = t, rest
		}
	} else if t, err := time.Parse(time.RFC3339Nano, s); err == nil { // empty line: timestamp only
		line.Time, line.Text = t, ""
	}
	return line
}
