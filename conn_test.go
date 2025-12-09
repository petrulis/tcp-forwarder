package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func assertEqual(t *testing.T, name string, got, want interface{}) {
	t.Helper() // marks this function as a helper for better line reporting
	if got != want {
		t.Errorf("expected %s=%v, got %v", name, want, got)
	}
}

func assertError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Errorf("expected error=%v, got %v", want, got)
	}
}

type testConn struct {
	r io.Reader
	w io.Writer
}

func (c *testConn) Read(p []byte) (int, error) {
	if c.r == nil {
		return 0, io.EOF
	}
	return c.r.Read(p)
}

func (c *testConn) Write(p []byte) (int, error) {
	if c.w == nil {
		return 0, io.EOF
	}
	return c.w.Write(p)
}

func (c *testConn) Close() error {
	return nil
}

func TestLimitedConn_Read(t *testing.T) {
	type input struct {
		uploaded uint64
		data     string
		bufSize  int
	}
	type expected struct {
		n        int
		err      error
		uploaded uint64
	}
	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "should write to connection",
			input: input{
				uploaded: 0,
				data:     "hello",
				bufSize:  10,
			},
			expected: expected{
				n:        5,
				err:      nil,
				uploaded: 5,
			},
		},
		{
			name: "should return error when read (upload) limit is reached",
			input: input{
				uploaded: 95,
				data:     "hello",
				bufSize:  10,
			},
			expected: expected{
				n:        5,
				err:      errUploadLimitExceeded,
				uploaded: 100,
			},
		},
		{
			name: "should truncate the data when limit is reached",
			input: input{
				uploaded: 90,
				data:     "hello world",
				bufSize:  20,
			},
			expected: expected{
				n:        10,
				err:      errUploadLimitExceeded,
				uploaded: 100,
			},
		},
		{
			name: "should return error immediately",
			input: input{
				uploaded: 100,
				data:     "hello",
				bufSize:  10,
			},
			expected: expected{
				n:        0,
				err:      errUploadLimitExceeded,
				uploaded: 100,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := newLimitedConn(&testConn{
				r: strings.NewReader(tt.input.data),
			}, 100)
			conn.uploaded = tt.input.uploaded
			buf := make([]byte, tt.input.bufSize)
			n, err := conn.Read(buf)

			// Assert if number of bytes read (n), error and uploaded bytes are as expected.
			assertEqual(t, "n", n, tt.expected.n)
			assertError(t, err, tt.expected.err)
			assertEqual(t, "uploaded", conn.uploaded, tt.expected.uploaded)
		})
	}
}

func TestLimitedConn_Write(t *testing.T) {
	type input struct {
		initialDownloaded uint64
		data              string
	}
	type expected struct {
		n          int
		err        error
		downloaded uint64
		written    string
	}
	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "normal write",
			input: input{
				initialDownloaded: 0,
				data:              "hello",
			},
			expected: expected{
				n:          5,
				err:        nil,
				downloaded: 5,
				written:    "hello",
			},
		},
		{
			name: "write reaches limit",
			input: input{
				initialDownloaded: 95,
				data:              "hello",
			},
			expected: expected{
				n:          5,
				err:        errDownloadLimitExceeded,
				downloaded: 100,
				written:    "hello",
			},
		},
		{
			name: "write truncated at limit",
			input: input{
				initialDownloaded: 90,
				data:              "hello world",
			},
			expected: expected{
				n:          10,
				err:        errDownloadLimitExceeded,
				downloaded: 100,
				written:    "hello worl",
			},
		},
		{
			name: "write when at limit",
			input: input{
				initialDownloaded: 100,
				data:              "hello",
			},
			expected: expected{
				n:          0,
				err:        errDownloadLimitExceeded,
				downloaded: 100,
				written:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			lc := newLimitedConn(&testConn{
				w: &buf,
			}, 100)
			lc.downloaded = tt.input.initialDownloaded

			n, err := lc.Write([]byte(tt.input.data))

			assertEqual(t, "n", n, tt.expected.n)
			assertError(t, err, tt.expected.err)
			assertEqual(t, "downloaded", lc.downloaded, tt.expected.downloaded)
			assertEqual(t, "written", buf.String(), tt.expected.written)
		})
	}
}

func TestLimitedConn_ConcurrentWrites(t *testing.T) {
	type input struct {
		numWriters int
		data       string
	}
	type expected struct {
		maxDownloaded uint64
	}
	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "normal write",
			input: input{
				data: "hello",
			},
			expected: expected{
				maxDownloaded: 5,
			},
		},
		{
			name: "many small writes hitting limit",
			input: input{
				numWriters: 20,
				data:       "hello",
			},
			expected: expected{
				maxDownloaded: 100,
			},
		},
		{
			name: "fewer larger writes hitting limit",
			input: input{
				numWriters: 5,
				data:       "this is a longer message",
			},
			expected: expected{
				maxDownloaded: 100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			conn := newLimitedConn(&testConn{
				w: &buf,
			}, 100)

			var wg sync.WaitGroup
			for i := 0; i < tt.input.numWriters; i++ {
				wg.Go(func() {
					conn.Write([]byte(tt.input.data))
				})
			}
			wg.Wait()

			if conn.downloaded > tt.expected.maxDownloaded {
				t.Errorf("downloaded %d bytes exceeds the limit %d", conn.downloaded, tt.expected.maxDownloaded)
			}
		})
	}
}
