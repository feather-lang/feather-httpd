# feather-httpd

A demo repository showing how to embed [Feather](https://www.feather-lang.dev) in a Go application.

This project implements a simple HTTP server that uses Feather scripts to handle requests, demonstrating Feather's embedding capabilities.

## Overview

- `main.go` - HTTP server setup and Feather interpreter integration
- `commands.go` - Custom commands exposed to Feather scripts
- `state.go` - Request/response state management
- `json.go` - JSON handling utilities
- `feather-httpd.tcl` - Example Feather script for handling HTTP requests
- `templates/` - HTML templates

## Running

```bash
go build
./feather-httpd
```

## Learn More

Visit [feather-lang.dev](https://www.feather-lang.dev) for documentation on the Feather language.
