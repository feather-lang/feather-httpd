package main

import (
	"bufio"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/feather-lang/feather"
)

//go:embed feather-httpd.tcl
var DefaultConfig string

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
		// Start interpreter loop in background
		go state.RunInterpreter(interp)
		// Start telnet REPL server on port 8081
		go runTelnetRepl(state)
		// Wait for shutdown
		<-state.shutdown
	}
}

func runTelnetRepl(state *ServerState) {
	listener, err := net.Listen("tcp", "127.0.0.1:8081")
	if err != nil {
		fmt.Fprintf(os.Stderr, "REPL listen error: %v\n", err)
		return
	}
	fmt.Println("REPL listening on 127.0.0.1:8081")

	// Close listener on shutdown
	go func() {
		<-state.shutdown
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return // listener closed
		}
		go func(c net.Conn) {
			defer c.Close()
			runRepl(state, c, c)
		}(conn)
	}
}

func runRepl(state *ServerState, r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	fmt.Fprint(w, "feather> ")

	var multiline strings.Builder
	for scanner.Scan() {
		line := scanner.Text()

		// Accumulate multiline input
		multiline.WriteString(line)
		multiline.WriteString("\n")

		input := strings.TrimSpace(multiline.String())
		if input == "" {
			fmt.Fprint(w, "feather> ")
			continue
		}

		// Check for balanced braces (simple heuristic for multiline)
		if !isComplete(input) {
			fmt.Fprint(w, "       > ")
			continue
		}

		result, err := state.EvalWithOutput(input, w)
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		} else if result.String() != "" {
			fmt.Fprintln(w, result.String())
		}

		multiline.Reset()
		fmt.Fprint(w, "feather> ")
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
