package main

import (
	"github.com/tliron/commonlog"
	_ "github.com/tliron/commonlog/simple"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"

	"github.com/akyrey/laravel-ls/internal/lsp"
)

const lsName = "laravel-lsp"

var version = "0.0.0-dev"

func main() {
	// Log to stderr at level 1 (info). Editors forward stderr to their
	// developer console or log file, so this is the right diagnostic sink.
	commonlog.Configure(1, nil)

	s := lsp.NewServer(commonlog.GetLogger(lsName), version)
	handler := buildHandler(s)
	srv := server.NewServer(handler, lsName, false)
	if err := srv.RunStdio(); err != nil {
		commonlog.GetLogger(lsName).Errorf("%s", err)
	}
}

func buildHandler(s *lsp.Server) *protocol.Handler {
	return &protocol.Handler{
		Initialize:  s.Initialize,
		Initialized: s.Initialized,
		Shutdown:    s.Shutdown,
		SetTrace:    s.SetTrace,

		TextDocumentDidOpen:   s.DidOpen,
		TextDocumentDidChange: s.DidChange,
		TextDocumentDidClose:  s.DidClose,

		TextDocumentDefinition: s.Definition,
		TextDocumentReferences: s.References,
		TextDocumentHover:      s.Hover,
		TextDocumentCompletion: s.Completion,
	}
}
