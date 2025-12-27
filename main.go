package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/feather-lang/feather"
)

func main() {
	scriptFile := flag.String("f", "feather-httpd.tcl", "TCL script file to load")
	noRepl := flag.Bool("no-repl", false, "Disable interactive REPL")
	flag.Parse()

	interp := feather.New()
	defer interp.Close()

	state := NewServerState()
	registerCommands(interp, state)

	// Handle SIGINT for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		close(state.shutdown)
		if state.server != nil {
			state.server.Close()
		}
	}()

	script, err := os.ReadFile(*scriptFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", *scriptFile, err)
		os.Exit(1)
	}

	// Eval startup script directly (before interpreter loop starts)
	_, err = interp.Eval(string(script))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *noRepl {
		// No REPL - just run the interpreter loop for HTTP requests
		state.RunInterpreter(interp)
	} else {
		// Start interpreter loop in background, run REPL in foreground
		go state.RunInterpreter(interp)
		runRepl(state)
	}
}

func runRepl(state *ServerState) {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("feather> ")

	var multiline strings.Builder
	for scanner.Scan() {
		line := scanner.Text()

		// Accumulate multiline input
		multiline.WriteString(line)
		multiline.WriteString("\n")

		input := strings.TrimSpace(multiline.String())
		if input == "" {
			fmt.Print("feather> ")
			continue
		}

		// Check for balanced braces (simple heuristic for multiline)
		if !isComplete(input) {
			fmt.Print("       > ")
			continue
		}

		result, err := state.Eval(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else if result.String() != "" {
			fmt.Println(result.String())
		}

		multiline.Reset()
		fmt.Print("feather> ")
	}
}

func isComplete(input string) bool {
	braces := 0
	brackets := 0
	inQuote := false
	escaped := false

	for _, c := range input {
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch c {
		case '{':
			braces++
		case '}':
			braces--
		case '[':
			brackets++
		case ']':
			brackets--
		}
	}
	return braces == 0 && brackets == 0 && !inQuote
}
