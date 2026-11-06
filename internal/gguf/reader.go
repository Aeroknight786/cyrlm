package gguf

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// readOptions tunes how a header is read.
type readOptions struct {
	// chunk is the first read size; each further read doubles it up to
	// maxChunk (a header is parsed front to back, so later reads are
	// usually continuations of a large tokenizer array).
	chunk    int
	maxChunk int
	// maxRead caps the total bytes read.
	maxRead int64
}

var defaultOptions = readOptions{chunk: 1 << 20, maxChunk: 4 << 20, maxRead: DefaultMaxHeaderBytes}

// Open parses the GGUF header of a local file. Only the header is read.
func Open(path string) (*File, error) {
	return openFile(path, defaultOptions)
}

func openFile(path string, o readOptions) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("gguf: %s is a directory", path)
	}
	f, err := parse(newWindowReader(fh, st.Size(), o))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f.Source = path
	return f, nil
}

// Parse parses a GGUF header from r. size is the total size of the
// underlying file, or -1 if unknown (bounds checks are then deferred to
// reads failing). At most DefaultMaxHeaderBytes are read.
func Parse(r io.ReaderAt, size int64) (*File, error) {
	return parse(newWindowReader(r, size, defaultOptions))
}

// windowReader serves small reads from a cached window of the underlying
// ReaderAt, so parsing costs one read (one HTTP request) per window rather
// than one per field. Returned slices are valid until the next call.
type windowReader struct {
	ra       io.ReaderAt
	size     int64 // -1 = unknown
	buf      []byte
	bufOff   int64
	chunk    int
	maxChunk int
	read     int64
	maxRead  int64
}

func newWindowReader(ra io.ReaderAt, size int64, o readOptions) *windowReader {
	if o.chunk <= 0 {
		o.chunk = defaultOptions.chunk
	}
	if o.maxChunk < o.chunk {
		o.maxChunk = o.chunk
	}
	if o.maxRead <= 0 {
		o.maxRead = DefaultMaxHeaderBytes
	}
	return &windowReader{ra: ra, size: size, chunk: o.chunk, maxChunk: o.maxChunk, maxRead: o.maxRead}
}

// prime installs already-read bytes at offset 0 as the current window.
func (w *windowReader) prime(b []byte) {
	w.buf, w.bufOff = b, 0
	w.read += int64(len(b))
	w.grow()
}

func (w *windowReader) grow() {
	if w.chunk < w.maxChunk {
		w.chunk *= 2
		if w.chunk > w.maxChunk {
			w.chunk = w.maxChunk
		}
	}
}

func truncated(off int64, n int, size int64) error {
	if size >= 0 {
		return fmt.Errorf("%w: need %d bytes at offset %d, file is %d bytes", ErrTruncated, n, off, size)
	}
	return fmt.Errorf("%w: need %d bytes at offset %d", ErrTruncated, n, off)
}

// slice returns n bytes at off.
func (w *windowReader) slice(off int64, n int) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	if off >= w.bufOff && off-w.bufOff <= int64(len(w.buf)) && int64(n) <= int64(len(w.buf))-(off-w.bufOff) {
		s := int(off - w.bufOff)
		return w.buf[s : s+n], nil
	}
	if off < 0 || (w.size >= 0 && (off > w.size || int64(n) > w.size-off)) {
		return nil, truncated(off, n, w.size)
	}
	want := n
	if want < w.chunk {
		want = w.chunk
	}
	if w.size >= 0 && int64(want) > w.size-off {
		want = int(w.size - off)
	}
	if rem := w.maxRead - w.read; int64(want) > rem {
		if int64(n) > rem {
			return nil, fmt.Errorf("%w: parsing needs more than %d bytes (offset %d)", ErrHeaderTooLarge, w.maxRead, off)
		}
		want = int(rem)
	}
	if cap(w.buf) < want {
		w.buf = make([]byte, want)
	}
	buf := w.buf[:want]
	m, err := w.ra.ReadAt(buf, off)
	if m < 0 || m > want {
		m = 0
	}
	w.read += int64(m)
	if m < n {
		w.buf = w.buf[:0]
		if err == nil || errors.Is(err, io.EOF) {
			return nil, truncated(off, n, w.size)
		}
		return nil, err
	}
	w.buf, w.bufOff = buf[:m], off
	w.grow()
	return w.buf[:n], nil
}
