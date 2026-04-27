package lsp

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/indexer/idehelper"
)

// Config holds settings passed via initializationOptions in the LSP initialize
// request. All directory lists are relative to the project root and support
// single-level glob patterns such as "Modules/*/app".
type Config struct {
	// ScanDirs are the directories walked when building the container and
	// Eloquent indexes. Defaults to ["app"].
	ScanDirs []string `json:"scanDirs"`

	// ReferenceDirs are the directories walked when searching for references
	// and rename sites. Defaults to ["app", "routes"].
	ReferenceDirs []string `json:"referenceDirs"`
}

func defaultConfig() Config {
	return Config{
		ScanDirs:      []string{"app"},
		ReferenceDirs: []string{"app", "routes"},
	}
}

// parseConfig decodes initializationOptions into a Config and fills in any
// fields that were not explicitly set:
//   - scanDirs defaults to ["app"]
//   - referenceDirs defaults to scanDirs + ["routes"], so that adding module
//     directories to scanDirs automatically covers them for reference search too.
//
// Setting referenceDirs explicitly in initializationOptions overrides the
// auto-derived value.
func parseConfig(opts any) (Config, error) {
	// Unmarshal into a zero struct so we can detect absent fields.
	var raw struct {
		ScanDirs      []string `json:"scanDirs"`
		ReferenceDirs []string `json:"referenceDirs"`
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return defaultConfig(), err
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return defaultConfig(), err
	}

	cfg := Config{
		ScanDirs:      raw.ScanDirs,
		ReferenceDirs: raw.ReferenceDirs,
	}
	if len(cfg.ScanDirs) == 0 {
		cfg.ScanDirs = []string{"app"}
	}
	if len(cfg.ReferenceDirs) == 0 {
		// Auto-derive: same dirs as scanning + routes.
		cfg.ReferenceDirs = append(append([]string{}, cfg.ScanDirs...), "routes")
	}
	return cfg, nil
}

// expandDirs expands glob patterns in patterns relative to root and returns
// a deduplicated list of directories that exist on disk.
// Non-glob patterns are included as-is (Walk handles missing dirs silently).
func expandDirs(root string, patterns []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range patterns {
		if !strings.ContainsAny(p, "*?[") {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, p))
		if err != nil {
			continue
		}
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				continue
			}
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
		}
	}
	return out
}

// reindexDebounce is how long to wait after the last PHP file change before
// triggering a reindex. Chosen to avoid thrashing during multi-file saves.
const reindexDebounce = 500 * time.Millisecond

// Server holds all LSP state: open document cache, indexes, and the project
// root. Handler methods are registered in main.go.
type Server struct {
	version string
	root    string
	cfg     Config
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
		cfg:     defaultConfig(),
		docs:    newDocumentStore(),
		log:     log,
	}
}

// Initialize detects the project root, reads initializationOptions, and
// returns server capabilities.
func (s *Server) Initialize(_ *glsp.Context, p *protocol.InitializeParams) (any, error) {
	root := detectRoot(p)

	cfg := defaultConfig()
	if p.InitializationOptions != nil {
		var err error
		cfg, err = parseConfig(p.InitializationOptions)
		if err != nil {
			s.log.Errorf("laravel-lsp: invalid initializationOptions: %v", err)
			cfg = defaultConfig()
		}
	}

	s.mu.Lock()
	s.root = root
	s.cfg = cfg
	s.mu.Unlock()
	s.log.Infof("laravel-lsp: root=%s scanDirs=%v referenceDirs=%v",
		root, cfg.ScanDirs, cfg.ReferenceDirs)

	syncKind := protocol.TextDocumentSyncKindFull
	ver := s.version
	triggerChars := []string{">"}
	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync:   syncKind,
			DefinitionProvider: true,
			ReferencesProvider: true,
			HoverProvider:      true,
			CodeActionProvider: &protocol.CodeActionOptions{
				CodeActionKinds: []protocol.CodeActionKind{
					protocol.CodeActionKindQuickFix,
				},
			},
			RenameProvider: &protocol.RenameOptions{
				PrepareProvider: boolPtr(true),
			},
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: triggerChars,
			},
			DocumentSymbolProvider: true,
		},
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    "laravel-lsp",
			Version: &ver,
		},
	}, nil
}

// Initialized kicks off background indexing then starts the file-watcher.
// The editor can send requests immediately; handlers return nil until indexing
// finishes.
func (s *Server) Initialized(_ *glsp.Context, _ *protocol.InitializedParams) error {
	s.scanOnce.Do(func() {
		go func() {
			s.mu.RLock()
			root := s.root
			s.mu.RUnlock()
			if root == "" {
				return
			}
			s.reindex(root)
			s.startWatcher(root)
		}()
	})
	return nil
}

// DidOpen caches the newly opened document and pushes diagnostics.
func (s *Server) DidOpen(ctx *glsp.Context, p *protocol.DidOpenTextDocumentParams) error {
	src := []byte(p.TextDocument.Text)
	s.docs.Set(p.TextDocument.URI, src)
	s.mu.RLock()
	models := s.models
	s.mu.RUnlock()
	path := URIToPath(p.TextDocument.URI)
	publishDiagnostics(ctx, p.TextDocument.URI, src, path, models)
	return nil
}

// DidChange updates the cached document and pushes diagnostics. Full sync: uses first change.
func (s *Server) DidChange(ctx *glsp.Context, p *protocol.DidChangeTextDocumentParams) error {
	if len(p.ContentChanges) == 0 {
		return nil
	}
	var src []byte
	switch c := p.ContentChanges[0].(type) {
	case protocol.TextDocumentContentChangeEventWhole:
		src = []byte(c.Text)
	case protocol.TextDocumentContentChangeEvent:
		src = []byte(c.Text)
	}
	if src == nil {
		return nil
	}
	s.docs.Set(p.TextDocument.URI, src)
	s.mu.RLock()
	models := s.models
	s.mu.RUnlock()
	path := URIToPath(p.TextDocument.URI)
	publishDiagnostics(ctx, p.TextDocument.URI, src, path, models)
	return nil
}

// DidClose removes the document from the cache and clears its diagnostics.
func (s *Server) DidClose(ctx *glsp.Context, p *protocol.DidCloseTextDocumentParams) error {
	s.docs.Delete(p.TextDocument.URI)
	// Clear diagnostics so they don't linger after the file is closed.
	publishDiagnostics(ctx, p.TextDocument.URI, nil, "", nil)
	return nil
}

// Shutdown is a no-op; cleanup happens at process exit.
func (s *Server) Shutdown(_ *glsp.Context) error { return nil }

// SetTrace is a no-op.
func (s *Server) SetTrace(_ *glsp.Context, _ *protocol.SetTraceParams) error { return nil }

// effectiveReferenceDirs returns the expanded reference scan directories for
// the given root, ready to pass to scanReferences / scanRenameRefs.
func (s *Server) effectiveReferenceDirs(root string) []string {
	s.mu.RLock()
	dirs := s.cfg.ReferenceDirs
	s.mu.RUnlock()
	return expandDirs(root, dirs)
}

// reindex rebuilds both indexes in parallel and atomically swaps them in.
func (s *Server) reindex(root string) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	scanDirs := expandDirs(root, cfg.ScanDirs)
	s.log.Infof("laravel-lsp: indexing %s (dirs: %v)", root, scanDirs)

	var wg sync.WaitGroup
	var bindings *container.BindingIndex
	var models *eloquent.ModelIndex
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		bindings, err1 = container.Walk(root, scanDirs)
	}()
	go func() {
		defer wg.Done()
		models, err2 = eloquent.Walk(root, scanDirs)
	}()
	wg.Wait()

	if err1 != nil {
		s.log.Errorf("laravel-lsp: container index: %v", err1)
	}
	if err2 != nil {
		s.log.Errorf("laravel-lsp: eloquent index: %v", err2)
	}

	// Augment the Eloquent index with ide-helper entries (no-op if file absent).
	if err2 == nil {
		ideHelperPath := filepath.Join(root, "_ide_helper_models.php")
		if err := idehelper.Merge(ideHelperPath, models); err != nil {
			s.log.Errorf("laravel-lsp: idehelper merge: %v", err)
		}
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
	if models != nil {
		for _, c := range models.All() {
			s.log.Debugf("laravel-lsp: indexed model %s (%s)", c.Class, c.Path)
		}
	}
}

// startWatcher watches the union of scan and reference directories under root
// for PHP file changes and triggers a debounced reindex on any write/create/remove.
func (s *Server) startWatcher(root string) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	// Watch the union of scan dirs and reference dirs so we catch changes
	// everywhere the server reads from.
	allPatterns := append(append([]string{}, cfg.ScanDirs...), cfg.ReferenceDirs...)
	watchSet := expandDirs(root, allPatterns)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		s.log.Errorf("laravel-lsp: watcher init: %v", err)
		return
	}

	for _, dir := range watchSet {
		scanDir := filepath.Join(root, dir)
		if _, err := os.Stat(scanDir); err != nil {
			continue
		}
		_ = filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			return w.Add(path)
		})
	}

	go s.watchLoop(w, root)
}

// watchLoop is the event loop for the fsnotify watcher. It accumulates changed
// PHP file paths during the debounce window, then calls reindexFiles with just
// those paths instead of a full walk.
func (s *Server) watchLoop(w *fsnotify.Watcher, root string) {
	defer w.Close()

	timer := time.NewTimer(reindexDebounce)
	timer.Stop()
	changedPaths := make(map[string]bool)

	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					_ = w.Add(event.Name)
				}
			}
			if strings.HasSuffix(event.Name, ".php") {
				changedPaths[event.Name] = true
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(reindexDebounce)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			s.log.Errorf("laravel-lsp: watcher: %v", err)
		case <-timer.C:
			paths := make([]string, 0, len(changedPaths))
			for p := range changedPaths {
				paths = append(paths, p)
			}
			changedPaths = make(map[string]bool)
			s.reindexFiles(root, paths)
		}
	}
}

// reindexFiles performs per-file incremental reindex for each changed path.
// Falls back to a full reindex when the index has no retained symbol table
// (e.g., first startup before any index exists).
func (s *Server) reindexFiles(root string, paths []string) {
	s.mu.RLock()
	bindings, models := s.bindings, s.models
	s.mu.RUnlock()

	if bindings == nil || models == nil || bindings.Syms() == nil || models.Syms() == nil {
		s.reindex(root)
		return
	}

	newBindings := bindings
	newModels := models

	for _, path := range paths {
		if nb, e := container.ReindexFile(path, newBindings); e != nil {
			s.log.Errorf("laravel-lsp: container reindex %s: %v", path, e)
		} else {
			newBindings = nb
		}

		if nm, e := eloquent.ReindexFile(path, newModels); e != nil {
			s.log.Errorf("laravel-lsp: eloquent reindex %s: %v", path, e)
		} else {
			newModels = nm
		}
	}

	if newBindings != bindings || newModels != models {
		s.mu.Lock()
		s.bindings = newBindings
		s.models = newModels
		s.mu.Unlock()
	}
	s.log.Infof("laravel-lsp: incremental reindex complete (%d files)", len(paths))
}

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
