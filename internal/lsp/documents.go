package lsp

import (
	"os"
	"sync"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// DocumentStore is an in-memory cache of open text documents. When a document
// is not open, Read falls back to the filesystem.
type DocumentStore struct {
	mu   sync.RWMutex
	docs map[protocol.DocumentUri][]byte
}

func newDocumentStore() *DocumentStore {
	return &DocumentStore{docs: make(map[protocol.DocumentUri][]byte)}
}

// Set stores or updates the content of an open document.
func (d *DocumentStore) Set(uri protocol.DocumentUri, content []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.docs[uri] = content
}

// Delete removes a document from the cache (called on DidClose).
func (d *DocumentStore) Delete(uri protocol.DocumentUri) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.docs, uri)
}

// Read returns the current content for uri. Falls back to disk when not cached.
func (d *DocumentStore) Read(uri protocol.DocumentUri) ([]byte, error) {
	d.mu.RLock()
	src, ok := d.docs[uri]
	d.mu.RUnlock()
	if ok {
		return src, nil
	}
	return os.ReadFile(URIToPath(uri))
}
