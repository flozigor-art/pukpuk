// Package proto defines the messages exchanged between crm-server (VPS) and
// crm-node (storage PC). See docs/PROTOCOL.md for the full description.
//
// The node always dials out; the server never connects to the node:
//
//	control:  WS   /api/node/ws                  JSON text frames, both directions
//	pull:     GET  /api/node/blob/{sha}          node downloads a buffered upload (Range resume)
//	stream:   WS   /api/node/stream/{id}         node pushes bytes the server asked for
//	derived:  WS   /api/node/derived/{sha}/{n}   node pushes a generated poster/thumb/preview
//	backup:   GET  /api/node/backup              node pulls a consistent DB snapshot
package proto

// Version is bumped on incompatible protocol changes.
const Version = 1

// Message types.
const (
	// node → server
	THello        = "hello"         // Hello
	TStats        = "stats"         // Stats
	TStored       = "stored"        // Sha + Size: blob saved and verified on the node
	TFetchFailed  = "fetch_failed"  // Sha + Error
	TMissing      = "missing"       // Shas: answer to Check, blobs the node does not have
	TProbe        = "probe"         // Probe: technical metadata of a blob
	TDeriveFailed = "derive_failed" // Sha + Error
	TServeError   = "serve_error"   // ID + Error: cannot serve a stream request

	// server → node
	TWelcome = "welcome" // server accepted the connection
	TFetch   = "fetch"   // Sha + Size: pull this blob from /api/node/blob/{sha}
	TServe   = "serve"   // ID + Sha + Off + Len: open /api/node/stream/{id} and send these bytes
	TDerive  = "derive"  // Sha + Mime + Want: generate derivatives and push them
	TCheck   = "check"   // Shas: report which of these you do not have
	TDelete  = "delete"  // Sha: move blob to the node trash
)

// Derived file names.
const (
	DerivedPoster  = "poster.jpg"  // up to 960px tall, video poster
	DerivedThumb   = "thumb.jpg"   // up to 320px tall, lists and grids
	DerivedPreview = "preview.mp4" // light H.264 proxy for playback
)

// Msg is the envelope of every control message. Only the fields relevant to
// the type are set.
type Msg struct {
	T     string   `json:"t"`
	ID    string   `json:"id,omitempty"`
	Sha   string   `json:"sha,omitempty"`
	Size  int64    `json:"size,omitempty"`
	Off   int64    `json:"off,omitempty"`
	Len   int64    `json:"len,omitempty"`
	Mime  string   `json:"mime,omitempty"`
	Want  []string `json:"want,omitempty"`
	Shas  []string `json:"shas,omitempty"`
	Error string   `json:"error,omitempty"`

	Hello *Hello `json:"hello,omitempty"`
	Stats *Stats `json:"stats,omitempty"`
	Probe *Probe `json:"probe,omitempty"`
}

type Hello struct {
	Proto   int    `json:"proto"`
	Version string `json:"version"`
	NodeID  string `json:"node_id"`
	Stats
}

type Stats struct {
	DiskFree  int64 `json:"disk_free"`
	DiskTotal int64 `json:"disk_total"`
	Blobs     int64 `json:"blobs"`
	Bytes     int64 `json:"bytes"`
	Queue     int   `json:"queue"`       // pending downloads
	Derive    int   `json:"derive"`      // pending derive jobs
	LastBack  int64 `json:"last_backup"` // unix ms of the last DB backup saved on the node
}

type Probe struct {
	DurationMs int64          `json:"duration_ms,omitempty"`
	Width      int            `json:"width,omitempty"`
	Height     int            `json:"height,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
	Peaks      []int          `json:"peaks,omitempty"`
}

// StreamHeader is the first (text) frame on a stream/derived websocket.
type StreamHeader struct {
	Size  int64  `json:"size"`            // full size of the blob
	Off   int64  `json:"off"`             // offset of the first byte that follows
	Len   int64  `json:"len"`             // number of bytes that follow
	Mime  string `json:"mime,omitempty"`  // for derived files
	Error string `json:"error,omitempty"` // set instead of data when the node cannot serve
}

// StreamEnd is the last (text) frame on a stream/derived websocket.
type StreamEnd struct {
	Done  bool   `json:"done"`
	Error string `json:"error,omitempty"`
}

// StreamChunk is the size of binary frames on data websockets.
const StreamChunk = 256 << 10

// ValidSha reports whether s looks like a lowercase hex sha256.
func ValidSha(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ValidDerived reports whether name is a known derived file name.
func ValidDerived(name string) bool {
	return name == DerivedPoster || name == DerivedThumb || name == DerivedPreview
}
