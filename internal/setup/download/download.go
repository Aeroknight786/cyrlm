// Package download fetches large files (model weights, engine release
// assets) with HTTP Range resume and a sha256 check, and reuses bytes that
// are already on disk (Hugging Face cache, LM Studio, Ollama blobs).
//
// P2 contract: the setup executor depends only on the exported API below.
// The dl agent replaces this stub with the implementation.
package download

import (
	"errors"
	"net/http"
	"time"
)

// Request is one file to fetch.
type Request struct {
	// URL is an https URL: a Hugging Face resolve URL
	// (https://huggingface.co/<repo>/resolve/<rev>/<file>) or a release asset.
	URL string
	// SHA256 is the expected lowercase hex digest. Required.
	SHA256 string
	// Size is the expected byte count; 0 means unknown.
	Size int64
	// Dest is the final path. Bytes go to Dest+".part" and are renamed into
	// place only after the digest matches.
	Dest string
	// Reuse lists local files that may already hold these bytes. The first
	// whose digest matches is linked (or copied) to Dest instead of
	// downloading. See Candidates.
	Reuse []string
	// Token is an optional Hugging Face token (sent only to huggingface.co).
	Token string
}

// Progress is reported while a file is fetched.
type Progress struct {
	Done, Total int64 // Total is 0 when unknown
	BytesPerSec float64
	ETA         time.Duration // 0 when unknown
	Reused      string        // set when the bytes came from a local file
}

// Result describes a finished fetch.
type Result struct {
	Path    string // == Request.Dest
	Reused  string // the local file that was reused, if any
	Bytes   int64  // bytes transferred over the network
	Resumed bool   // true when an existing .part was continued
}

// Client fetches files. The zero value is usable.
type Client struct {
	HTTP     *http.Client   // nil = a client with sane timeouts
	Progress func(Progress) // optional, called at most a few times a second
}

// ErrDigest is returned when the bytes do not match Request.SHA256 after
// one automatic re-fetch.
var ErrDigest = errors.New("download: sha256 mismatch")
