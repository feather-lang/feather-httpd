<div align="center">

# feather-httpd

<img src="https://www.feather-lang.dev/logo.png" alt="Feather Logo" height="80" />
&nbsp;&nbsp;&nbsp;&nbsp;
<img src="https://go.dev/images/gophers/blue.svg" alt="Go Gopher" height="80" />

**A demo project showing how to embed [Feather](https://www.feather-lang.dev) in a Go application**

This is a toy HTTP server that uses Feather scripts to handle requests, demonstrating Feather's embedding capabilities.

</div>

---

## See It In Action

https://github.com/user-attachments/assets/demo.webm

<video src="demo.webm" controls width="100%"></video>

---

## What This Demonstrates

- Embedding the Feather interpreter in a Go application
- Exposing Go functions as Feather commands
- Live code reloading via a built-in REPL
- Template rendering from scripts
- Streaming responses (Server-Sent Events)

## Quick Start

```bash
go build
./feather-httpd
```

Visit `http://localhost:8080` to see the server running.

### Connecting to the REPL

The server exposes a REPL on port 8081. Connect with readline support using `rlwrap` and `nc`:

```bash
rlwrap nc localhost 8081
```

Or use the web-based REPL at `http://localhost:8080/_repl`.

## Project Structure

```
feather-httpd/
├── main.go           # HTTP server setup, REPL server, and Feather interpreter initialization
├── commands.go       # Custom commands exposed to Feather scripts (route, respond, template, etc.)
├── state.go          # Request/response state management and route registry
├── json.go           # JSON parsing and encoding utilities
├── feather-httpd.tcl # Example Feather script — your application logic lives here
└── templates/        # HTML templates directory
```

### How It Works

```
┌─────────────────────────────────────────────────────────────┐
│                        Go Runtime                           │
│  ┌─────────────────┐    ┌─────────────────────────────────┐ │
│  │   HTTP Server   │    │      Feather Interpreter        │ │
│  │   (port 8080)   │───▶│  • Evaluates route handlers     │ │
│  └─────────────────┘    │  • Manages templates            │ │
│  ┌─────────────────┐    │  • Processes JSON               │ │
│  │   REPL Server   │───▶│                                 │ │
│  │   (port 8081)   │    └─────────────────────────────────┘ │
│  └─────────────────┘                                        │
└─────────────────────────────────────────────────────────────┘
```

1. **main.go** boots the HTTP server and initializes the Feather interpreter
2. **commands.go** registers Go functions as Feather commands (`route`, `respond`, `template`, etc.)
3. **state.go** maintains thread-safe state for routes, templates, and active connections
4. **feather-httpd.tcl** is loaded at startup — define your routes here

## Example Script

```tcl
# Define a simple route
route GET /hello {
    respond "Hello, World!"
}

# Use templates with data
route GET /greet/:name {
    template respond greeting name [param name]
}

# Return JSON
route GET /api/status {
    header Content-Type application/json
    respond [json encode {status ok}]
}
```

## Learn More

Visit [feather-lang.dev](https://www.feather-lang.dev) for Feather language documentation.
