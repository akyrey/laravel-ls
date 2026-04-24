package main

import (
	"fmt"
	"os"

	"github.com/tliron/commonlog"
	_ "github.com/tliron/commonlog/simple"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"

	"github.com/akyrey/laravel-lsp/internal/lsp"
)

const lsName = "laravel-lsp"

var version = "0.0.0-dev"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "debug":
			runDebug(os.Args[2:])
			return
		case "version", "--version", "-version":
			fmt.Println(version)
			return
		case "help", "--help", "-help", "-h":
			fmt.Fprintf(os.Stderr, "Usage: laravel-lsp [debug [flags] [project-root]]\n\nRun without arguments to start the LSP server over stdio.\nRun 'laravel-lsp debug --help' for the index inspection tool.\n")
			return
		}
	}

	// Default: run LSP server over stdio.
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

		TextDocumentDefinition:    s.Definition,
		TextDocumentReferences:    s.References,
		TextDocumentHover:         s.Hover,
		TextDocumentCompletion:    s.Completion,
		TextDocumentRename:        s.Rename,
		TextDocumentPrepareRename: s.PrepareRename,
		TextDocumentCodeAction:    s.CodeAction,
		TextDocumentDocumentSymbol: s.DocumentSymbol,
	}
}
