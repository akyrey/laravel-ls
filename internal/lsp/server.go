package lsp

import (
	"os"
	"sync"

	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

// Server holds all LSP state: open document cache, indexes, and the project
// root. Handler methods are registered in main.go.
type Server struct {
	version string
	root    string
	docs    *DocumentStore
	log     commonlog.Logger

	mu       sync.RWMutex
	scanOnce sync.Once
	bindings *container.BindingIndex
	models   *eloquent.ModelIndex
}

// NewServer creates a Server ready to accept LSP requests.
func NewServer(log commonlog.Logger, version string) *Server {
	return &Server{
		version: version,
		docs:    newDocumentStore(),
		log:     log,
	}
}

// Initialize detects the project root and returns server capabilities.
func (s *Server) Initialize(_ *glsp.Context, p *protocol.InitializeParams) (any, error) {
	root := detectRoot(p)
	s.mu.Lock()
	s.root = root
	s.mu.Unlock()
	s.log.Infof("laravel-lsp: root=%s", root)

	syncKind := protocol.TextDocumentSyncKindFull
	ver := s.version
	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync:   syncKind,
			DefinitionProvider: true,
			ReferencesProvider: true,
		},
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    "laravel-lsp",
			Version: &ver,
		},
	}, nil
}

// Initialized kicks off background indexing. The editor can start sending
// requests immediately; handlers return nil until indexing finishes.
func (s *Server) Initialized(_ *glsp.Context, _ *protocol.InitializedParams) error {
	s.scanOnce.Do(func() {
		go func() {
			s.mu.RLock()
			root := s.root
			s.mu.RUnlock()
			if root == "" {
				return
			}
			s.log.Infof("laravel-lsp: indexing %s", root)

			var wg sync.WaitGroup
			var bindings *container.BindingIndex
			var models *eloquent.ModelIndex
			var err1, err2 error

			wg.Add(2)
			go func() {
				defer wg.Done()
				bindings, err1 = container.Walk(root, container.DefaultScanDirs)
			}()
			go func() {
				defer wg.Done()
				models, err2 = eloquent.Walk(root, eloquent.DefaultScanDirs)
			}()
			wg.Wait()

			if err1 != nil {
				s.log.Errorf("laravel-lsp: container index: %v", err1)
			}
			if err2 != nil {
				s.log.Errorf("laravel-lsp: eloquent index: %v", err2)
			}

			s.mu.Lock()
			if err1 == nil {
				s.bindings = bindings
			}
			if err2 == nil {
				s.models = models
			}
			s.mu.Unlock()
			s.log.Infof("laravel-lsp: indexing complete")
		}()
	})
	return nil
}

// DidOpen caches the newly opened document.
func (s *Server) DidOpen(_ *glsp.Context, p *protocol.DidOpenTextDocumentParams) error {
	s.docs.Set(p.TextDocument.URI, []byte(p.TextDocument.Text))
	return nil
}

// DidChange updates the cached document. Full sync: uses the first change entry.
func (s *Server) DidChange(_ *glsp.Context, p *protocol.DidChangeTextDocumentParams) error {
	if len(p.ContentChanges) == 0 {
		return nil
	}
	switch c := p.ContentChanges[0].(type) {
	case protocol.TextDocumentContentChangeEventWhole:
		s.docs.Set(p.TextDocument.URI, []byte(c.Text))
	case protocol.TextDocumentContentChangeEvent:
		s.docs.Set(p.TextDocument.URI, []byte(c.Text))
	}
	return nil
}

// DidClose removes the document from the cache.
func (s *Server) DidClose(_ *glsp.Context, p *protocol.DidCloseTextDocumentParams) error {
	s.docs.Delete(p.TextDocument.URI)
	return nil
}

// Shutdown is a no-op; cleanup happens at process exit.
func (s *Server) Shutdown(_ *glsp.Context) error { return nil }

// SetTrace is a no-op.
func (s *Server) SetTrace(_ *glsp.Context, _ *protocol.SetTraceParams) error { return nil }

// detectRoot extracts the project root from InitializeParams.
// Priority: WorkspaceFolders[0] > RootURI > RootPath > cwd.
func detectRoot(p *protocol.InitializeParams) string {
	if len(p.WorkspaceFolders) > 0 {
		return URIToPath(p.WorkspaceFolders[0].URI)
	}
	if p.RootURI != nil && *p.RootURI != "" {
		return URIToPath(*p.RootURI)
	}
	if p.RootPath != nil && *p.RootPath != "" {
		return *p.RootPath
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
