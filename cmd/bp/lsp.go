package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/blueprint/internal/lsp"
)

func cmdLSP() int {
	fmt.Fprintln(os.Stderr, "Blueprint LSP server starting...")
	
	server := lsp.NewServer(os.Stdin, os.Stdout)
	if err := server.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "LSP error: %v\n", err)
		return 1
	}
	return 0
}
