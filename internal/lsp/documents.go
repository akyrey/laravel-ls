package lsp

import (
	"os"
	"sync"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// DocumentStore is an in-memory cache of open text documents. When a document
// is not open, Read falls back to the filesystem.
//
// Entries are keyed by decoded filesystem path, not by the raw URI string:
// clients may percent-encode URIs (file:///My%20Project/...) while internal
// callers build them from paths via PathToURI, and both forms must hit the
// same cache entry.
type DocumentStore struct {
	mu   sync.RWMutex
	docs map[string][]byte
}

func newDocumentStore() *DocumentStore {
	return &DocumentStore{docs: make(map[string][]byte)}
}

// Set stores or updates the content of an open document.
func (d *DocumentStore) Set(uri protocol.DocumentUri, content []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.docs[URIToPath(uri)] = content
}

// Delete removes a document from the cache (called on DidClose).
func (d *DocumentStore) Delete(uri protocol.DocumentUri) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.docs, URIToPath(uri))
}

// Read returns the current content for uri. Falls back to disk when not cached.
func (d *DocumentStore) Read(uri protocol.DocumentUri) ([]byte, error) {
	path := URIToPath(uri)
	d.mu.RLock()
	src, ok := d.docs[path]
	d.mu.RUnlock()
	if ok {
		return src, nil
	}
	return os.ReadFile(path)
}
