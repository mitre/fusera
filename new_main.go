package main

import (
	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/cmd"
)

func init() {
	// Customize the format of log messages
	twig.SetFlags(twig.LstdFlags | twig.Lshortfile)
}

func main() {
	cmd.Execute()
}
