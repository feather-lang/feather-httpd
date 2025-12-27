package main

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/feather-lang/feather"
)

// Command represents a command or subcommand with help text and optional children
type Command struct {
	Name        string
	Help        string
	Usage       string
	Subcommands []*Command
	Handler     func(*feather.Interp, feather.Object, []feather.Object) feather.Result
}

// FindSubcommand looks up a subcommand by name
func (c *Command) FindSubcommand(name string) *Command {
	for _, sub := range c.Subcommands {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}

// FormatHelp returns formatted help text for this command
func (c *Command) FormatHelp(prefix string) string {
	var sb strings.Builder
	if c.Usage != "" {
		sb.WriteString(fmt.Sprintf("%s%s - %s\n", prefix, c.Usage, c.Help))
	} else {
		sb.WriteString(fmt.Sprintf("%s%s - %s\n", prefix, c.Name, c.Help))
	}
	for _, sub := range c.Subcommands {
		subPrefix := prefix + "  "
		sb.WriteString(sub.FormatHelp(subPrefix))
	}
	return sb.String()
}

// CommandRegistry holds all registered commands
type CommandRegistry struct {
	commands []*Command
}

// Register adds a command to the registry
func (r *CommandRegistry) Register(cmd *Command) {
	r.commands = append(r.commands, cmd)
}

// Find looks up a command by name
func (r *CommandRegistry) Find(name string) *Command {
	for _, cmd := range r.commands {
		if cmd.Name == name {
			return cmd
		}
	}
	return nil
}

// All returns all registered commands
func (r *CommandRegistry) All() []*Command {
	return r.commands
}

var registry = &CommandRegistry{}

func registerCommands(interp *feather.Interp, state *ServerState) {
	registerJSONCommand(interp, state)
	// Route command
	routeCmd := &Command{
		Name:  "route",
		Help:  "Define a route handler",
		Usage: "route METHOD PATH BODY",
	}
	registry.Register(routeCmd)
	interp.Register("route", func(method, pattern, body string) error {
		state.AddRoute(method, pattern, body)
		return nil
	})

	// Respond command
	respondCmd := &Command{
		Name:  "respond",
		Help:  "Write response body to client",
		Usage: "respond ?-to HANDLE? BODY",
	}
	registry.Register(respondCmd)
	interp.RegisterCommand("respond", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		var ctx *RequestContext
		bodyIdx := 0

		// Check for -to HANDLE
		if len(args) >= 2 && args[0].String() == "-to" {
			handle := args[1].String()
			conn := state.GetConnection(handle)
			if conn == nil {
				// Connection gone, silently succeed
				return feather.OK("")
			}
			ctx = conn.Ctx
			bodyIdx = 2
		} else {
			ctx = state.GetRequestContext()
			if ctx == nil {
				return feather.Error("respond: not in request context")
			}
		}
		if len(args) <= bodyIdx {
			return feather.Error("wrong # args: should be \"respond ?-to handle? body\"")
		}

		ctx.mu.Lock()
		defer ctx.mu.Unlock()

		if !ctx.Written {
			ctx.Headers.Range(func(k, v any) bool {
				ctx.Writer.Header().Set(k.(string), v.(string))
				return true
			})
			if ctx.Status != 0 {
				ctx.Writer.WriteHeader(ctx.Status)
			}
			ctx.Written = true
		}

		body := args[bodyIdx].String()
		ctx.Writer.Write([]byte(body))
		return feather.OK("")
	})

	// Status command
	statusCmd := &Command{
		Name:  "status",
		Help:  "Set HTTP response status code",
		Usage: "status ?-to HANDLE? CODE",
	}
	registry.Register(statusCmd)
	interp.RegisterCommand("status", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		var ctx *RequestContext
		codeIdx := 0

		// Check for -to HANDLE
		if len(args) >= 2 && args[0].String() == "-to" {
			handle := args[1].String()
			conn := state.GetConnection(handle)
			if conn == nil {
				return feather.OK("")
			}
			ctx = conn.Ctx
			codeIdx = 2
		} else {
			ctx = state.GetRequestContext()
			if ctx == nil {
				return feather.Error("status: not in request context")
			}
		}
		if len(args) <= codeIdx {
			return feather.Error("wrong # args: should be \"status ?-to handle? code\"")
		}
		code, err := args[codeIdx].Int()
		if err != nil {
			return feather.Errorf("status: expected integer, got %s", args[codeIdx].String())
		}
		ctx.mu.Lock()
		ctx.Status = int(code)
		ctx.mu.Unlock()
		return feather.OK("")
	})

	// Header command
	headerCmd := &Command{
		Name:  "header",
		Help:  "Set HTTP response header",
		Usage: "header ?-to HANDLE? NAME VALUE",
	}
	registry.Register(headerCmd)
	interp.RegisterCommand("header", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		var ctx *RequestContext
		nameIdx := 0

		// Check for -to HANDLE
		if len(args) >= 2 && args[0].String() == "-to" {
			handle := args[1].String()
			conn := state.GetConnection(handle)
			if conn == nil {
				return feather.OK("")
			}
			ctx = conn.Ctx
			nameIdx = 2
		} else {
			ctx = state.GetRequestContext()
			if ctx == nil {
				return feather.Error("header: not in request context")
			}
		}
		if len(args) < nameIdx+2 {
			return feather.Error("wrong # args: should be \"header ?-to handle? name value\"")
		}
		ctx.Headers.Store(args[nameIdx].String(), args[nameIdx+1].String())
		return feather.OK("")
	})

	// Param command
	paramCmd := &Command{
		Name:  "param",
		Help:  "Get path parameter from URL",
		Usage: "param NAME",
	}
	registry.Register(paramCmd)
	interp.RegisterCommand("param", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		ctx := state.GetRequestContext()
		if ctx == nil {
			return feather.Error("param: not in request context")
		}
		if len(args) < 1 {
			return feather.Error("wrong # args: should be \"param name\"")
		}
		name := args[0].String()
		if val, ok := ctx.Params[name]; ok {
			return feather.OK(val)
		}
		return feather.OK("")
	})

	// Query command
	queryCmd := &Command{
		Name:  "query",
		Help:  "Get query string parameter",
		Usage: "query NAME ?DEFAULT?",
	}
	registry.Register(queryCmd)
	interp.RegisterCommand("query", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		ctx := state.GetRequestContext()
		if ctx == nil {
			return feather.Error("query: not in request context")
		}
		if len(args) < 1 {
			return feather.Error("wrong # args: should be \"query name ?default?\"")
		}
		name := args[0].String()
		val := ctx.Request.URL.Query().Get(name)
		if val == "" && len(args) > 1 {
			val = args[1].String()
		}
		return feather.OK(val)
	})

	// Path command with subcommands
	pathCmd := &Command{
		Name:  "path",
		Help:  "File path manipulation utilities",
		Usage: "path SUBCOMMAND ?ARG ...?",
		Subcommands: []*Command{
			{Name: "join", Help: "Join path elements", Usage: "path join PART ?PART ...?"},
			{Name: "base", Help: "Return last element of path", Usage: "path base PATH"},
			{Name: "dir", Help: "Return directory portion of path", Usage: "path dir PATH"},
			{Name: "ext", Help: "Return file extension", Usage: "path ext PATH"},
			{Name: "clean", Help: "Return cleaned path", Usage: "path clean PATH"},
			{Name: "abs", Help: "Return absolute path", Usage: "path abs PATH"},
			{Name: "exists", Help: "Return 1 if path exists, 0 otherwise", Usage: "path exists PATH"},
		},
	}
	registry.Register(pathCmd)
	interp.RegisterCommand("path", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		if len(args) < 1 {
			return feather.Error("wrong # args: should be \"path subcommand ?arg ...?\"")
		}
		subcmd := args[0].String()
		switch subcmd {
		case "join":
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"path join part ?part ...?\"")
			}
			parts := make([]string, len(args)-1)
			for i, arg := range args[1:] {
				parts[i] = arg.String()
			}
			return feather.OK(filepath.Join(parts...))
		case "base":
			if len(args) != 2 {
				return feather.Error("wrong # args: should be \"path base path\"")
			}
			return feather.OK(filepath.Base(args[1].String()))
		case "dir":
			if len(args) != 2 {
				return feather.Error("wrong # args: should be \"path dir path\"")
			}
			return feather.OK(filepath.Dir(args[1].String()))
		case "ext":
			if len(args) != 2 {
				return feather.Error("wrong # args: should be \"path ext path\"")
			}
			return feather.OK(filepath.Ext(args[1].String()))
		case "clean":
			if len(args) != 2 {
				return feather.Error("wrong # args: should be \"path clean path\"")
			}
			return feather.OK(filepath.Clean(args[1].String()))
		case "abs":
			if len(args) != 2 {
				return feather.Error("wrong # args: should be \"path abs path\"")
			}
			abs, err := filepath.Abs(args[1].String())
			if err != nil {
				return feather.Errorf("path abs: %v", err)
			}
			return feather.OK(abs)
		case "exists":
			if len(args) != 2 {
				return feather.Error("wrong # args: should be \"path exists path\"")
			}
			_, err := os.Stat(args[1].String())
			if err == nil {
				return feather.OK(1)
			}
			return feather.OK(0)
		default:
			return feather.Errorf("path: unknown subcommand %q (must be join, base, dir, ext, clean, abs, exists)", subcmd)
		}
	})

	// Template command with subcommands
	templateCmd := &Command{
		Name:  "template",
		Help:  "HTML template operations",
		Usage: "template SUBCOMMAND ?ARG ...?",
		Subcommands: []*Command{
			{Name: "define", Help: "Define a template inline", Usage: "template define NAME CONTENT"},
			{Name: "load", Help: "Load template from file", Usage: "template load PATH ?as NAME?"},
			{Name: "loaddir", Help: "Load all templates from directory", Usage: "template loaddir DIR ?GLOB?"},
			{Name: "list", Help: "List loaded template names", Usage: "template list"},
			{Name: "show", Help: "Show template source", Usage: "template show NAME"},
			{Name: "respond", Help: "Render template to HTTP response", Usage: "template respond NAME ?KEY VAL ...?"},
			{Name: "string", Help: "Render template to string", Usage: "template string NAME ?KEY VAL ...?"},
		},
	}
	registry.Register(templateCmd)
	interp.RegisterCommand("template", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		if len(args) < 1 {
			return feather.Error("wrong # args: should be \"template subcommand ?arg ...?\"")
		}
		subcmd := args[0].String()
		switch subcmd {
		case "define":
			// template define NAME CONTENT
			if len(args) < 3 {
				return feather.Error("wrong # args: should be \"template define name content\"")
			}
			name := args[1].String()
			content := args[2].String()
			if err := state.LoadTemplate(name, content); err != nil {
				return feather.Errorf("template define: %v", err)
			}
			if err := state.ReparseTemplates(); err != nil {
				return feather.Errorf("template define: %v", err)
			}
			return feather.OK(name)

		case "load":
			// template load PATH ?AS NAME?
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"template load path ?as name?\"")
			}
			tmplPath := args[1].String()
			name := strings.TrimSuffix(filepath.Base(tmplPath), filepath.Ext(tmplPath))

			// Check for "as NAME"
			if len(args) >= 4 && args[2].String() == "as" {
				name = args[3].String()
			}

			content, err := os.ReadFile(tmplPath)
			if err != nil {
				return feather.Errorf("template load: %v", err)
			}

			if err := state.LoadTemplate(name, string(content)); err != nil {
				return feather.Errorf("template load: %v", err)
			}
			if err := state.ReparseTemplates(); err != nil {
				return feather.Errorf("template load: %v", err)
			}
			return feather.OK(name)

		case "loaddir":
			// template loaddir DIR ?GLOB?
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"template loaddir dir ?glob?\"")
			}
			dir := args[1].String()
			glob := "*.html"
			if len(args) >= 3 {
				glob = args[2].String()
			}

			pattern := filepath.Join(dir, glob)
			files, err := filepath.Glob(pattern)
			if err != nil {
				return feather.Errorf("template loaddir: %v", err)
			}

			var loaded []string
			for _, file := range files {
				name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
				content, err := os.ReadFile(file)
				if err != nil {
					return feather.Errorf("template loaddir: %v", err)
				}
				if err := state.LoadTemplate(name, string(content)); err != nil {
					return feather.Errorf("template loaddir %s: %v", name, err)
				}
				loaded = append(loaded, name)
			}
			if err := state.ReparseTemplates(); err != nil {
				return feather.Errorf("template loaddir: %v", err)
			}
			return feather.OK(strings.Join(loaded, " "))

		case "list":
			names := state.ListTemplates()
			sort.Strings(names)
			return feather.OK(strings.Join(names, " "))

		case "show":
			// template show NAME
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"template show name\"")
			}
			name := args[1].String()
			src := state.GetTemplateSource(name)
			if src == "" {
				return feather.Errorf("template show: unknown template %q", name)
			}
			return feather.OK(src)

		case "respond":
			// template respond NAME key val key val ...
			// template respond NAME dict
			ctx := state.GetRequestContext()
			if ctx == nil {
				return feather.Error("template respond: not in request context")
			}
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"template respond name ?key val ...?\"")
			}
			name := args[1].String()
			tmpl := state.GetTemplate(name)
			if tmpl == nil {
				return feather.Errorf("template respond: unknown template %q", name)
			}

			data, err := parseTemplateData(args[2:])
			if err != nil {
				return feather.Errorf("template respond: %v", err)
			}

			ctx.mu.Lock()
			defer ctx.mu.Unlock()

			if _, ok := ctx.Headers.Load("Content-Type"); !ok {
				ctx.Headers.Store("Content-Type", "text/html; charset=utf-8")
			}
			ctx.Headers.Range(func(k, v any) bool {
				ctx.Writer.Header().Set(k.(string), v.(string))
				return true
			})
			if ctx.Status != 0 {
				ctx.Writer.WriteHeader(ctx.Status)
			}
			ctx.Written = true

			if err := tmpl.Execute(ctx.Writer, data); err != nil {
				return feather.Errorf("template respond: %v", err)
			}
			return feather.OK("")

		case "string":
			// template string NAME key val key val ...
			// template string NAME dict
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"template string name ?key val ...?\"")
			}
			name := args[1].String()
			tmpl := state.GetTemplate(name)
			if tmpl == nil {
				return feather.Errorf("template string: unknown template %q", name)
			}

			data, err := parseTemplateData(args[2:])
			if err != nil {
				return feather.Errorf("template string: %v", err)
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				return feather.Errorf("template string: %v", err)
			}
			return feather.OK(buf.String())

		default:
			return feather.Errorf("template: unknown subcommand %q (must be load, loaddir, list, respond, string)", subcmd)
		}
	})

	// Sendfile command
	sendfileCmd := &Command{
		Name:  "sendfile",
		Help:  "Serve file content with auto-detected MIME type",
		Usage: "sendfile PATH",
	}
	registry.Register(sendfileCmd)
	interp.RegisterCommand("sendfile", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		ctx := state.GetRequestContext()
		if ctx == nil {
			return feather.Error("sendfile: not in request context")
		}
		if len(args) < 1 {
			return feather.Error("wrong # args: should be \"sendfile path\"")
		}
		filepath := args[0].String()

		file, err := os.Open(filepath)
		if err != nil {
			return feather.Errorf("sendfile: %v", err)
		}
		defer file.Close()

		stat, err := file.Stat()
		if err != nil {
			return feather.Errorf("sendfile: %v", err)
		}

		ctx.mu.Lock()
		defer ctx.mu.Unlock()

		if _, ok := ctx.Headers.Load("Content-Type"); !ok {
			ct := mime.TypeByExtension(path.Ext(filepath))
			if ct == "" {
				ct = "application/octet-stream"
			}
			ctx.Headers.Store("Content-Type", ct)
		}

		ctx.Headers.Range(func(k, v any) bool {
			ctx.Writer.Header().Set(k.(string), v.(string))
			return true
		})
		if ctx.Status != 0 {
			ctx.Writer.WriteHeader(ctx.Status)
		}
		ctx.Written = true

		http.ServeContent(ctx.Writer, ctx.Request, filepath, stat.ModTime(), file)
		return feather.OK("")
	})

	// Request command with subcommands
	requestCmd := &Command{
		Name:  "request",
		Help:  "Access request data",
		Usage: "request SUBCOMMAND ?ARG?",
		Subcommands: []*Command{
			{Name: "method", Help: "Get HTTP method", Usage: "request method"},
			{Name: "path", Help: "Get request path", Usage: "request path"},
			{Name: "body", Help: "Get request body", Usage: "request body"},
			{Name: "header", Help: "Get request header", Usage: "request header NAME"},
		},
	}
	registry.Register(requestCmd)
	interp.RegisterCommand("request", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		ctx := state.GetRequestContext()
		if ctx == nil {
			return feather.Error("request: not in request context")
		}
		if len(args) < 1 {
			return feather.Error("wrong # args: should be \"request subcommand ?arg?\"")
		}
		subcmd := args[0].String()
		switch subcmd {
		case "method":
			return feather.OK(ctx.Request.Method)
		case "path":
			return feather.OK(ctx.Request.URL.Path)
		case "body":
			body, err := io.ReadAll(ctx.Request.Body)
			if err != nil {
				return feather.Errorf("request body: %v", err)
			}
			return feather.OK(string(body))
		case "header":
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"request header name\"")
			}
			return feather.OK(ctx.Request.Header.Get(args[1].String()))
		default:
			return feather.Errorf("request: unknown subcommand %q", subcmd)
		}
	})

	// Puts command
	putsCmd := &Command{
		Name:  "puts",
		Help:  "Print string to output",
		Usage: "puts STRING",
	}
	registry.Register(putsCmd)
	interp.RegisterCommand("puts", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		if len(args) < 1 {
			return feather.Error("wrong # args: should be \"puts string\"")
		}
		msg := args[0].String()
		if evalCtx := state.GetEvalContext(); evalCtx != nil && evalCtx.Output != nil {
			evalCtx.Output(msg)
		} else {
			fmt.Println(msg)
		}
		return feather.OK("")
	})

	// Routes command
	routesCmd := &Command{
		Name:  "routes",
		Help:  "List all defined routes",
		Usage: "routes",
	}
	registry.Register(routesCmd)
	interp.RegisterCommand("routes", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		routes := state.GetRoutes()
		var items []string
		for _, r := range routes {
			// Each item is a properly quoted list element
			item := fmt.Sprintf("route %s %s {%s}", r.Method, r.Pattern, r.Body)
			items = append(items, item)
		}
		return feather.OK(items)
	})

	// Listen command
	listenCmd := &Command{
		Name:  "listen",
		Help:  "Start the HTTP server on specified port",
		Usage: "listen PORT",
	}
	registry.Register(listenCmd)
	interp.Register("listen", func(port int) error {
		addr := fmt.Sprintf(":%d", port)
		state.server = &http.Server{
			Addr:    addr,
			Handler: createHandler(state),
		}

		fmt.Printf("Listening on %s\n", addr)
		go func() {
			if err := state.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Printf("Server error: %v\n", err)
			}
		}()

		return nil
	})

	// Shutdown command
	shutdownCmd := &Command{
		Name:  "shutdown",
		Help:  "Stop the server gracefully",
		Usage: "shutdown",
	}
	registry.Register(shutdownCmd)
	interp.Register("shutdown", func() error {
		close(state.shutdown)
		if state.server != nil {
			return state.server.Close()
		}
		return nil
	})

	// Help command
	helpCmd := &Command{
		Name:  "help",
		Help:  "Show help for commands",
		Usage: "help ?COMMAND? ?SUBCOMMAND ...?",
	}
	registry.Register(helpCmd)
	interp.RegisterCommand("help", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		output := func(msg string) {
			if evalCtx := state.GetEvalContext(); evalCtx != nil && evalCtx.Output != nil {
				evalCtx.Output(msg)
			} else {
				fmt.Println(msg)
			}
		}

		if len(args) == 0 {
			// List all commands
			output("Available commands:")
			for _, c := range registry.All() {
				output(c.FormatHelp("  "))
			}
			return feather.Result{}
		}

		// Find specific command
		cmdName := args[0].String()
		c := registry.Find(cmdName)
		if c == nil {
			return feather.Errorf("help: unknown command %q", cmdName)
		}

		// Navigate to subcommand if specified
		for _, arg := range args[1:] {
			subName := arg.String()
			sub := c.FindSubcommand(subName)
			if sub == nil {
				return feather.Errorf("help: %s has no subcommand %q", c.Name, subName)
			}
			c = sub
		}

		output(c.FormatHelp(""))
		return feather.Result{}
	})

	// Connection command with subcommands
	connectionCmd := &Command{
		Name:  "connection",
		Help:  "Manage held HTTP connections for streaming",
		Usage: "connection SUBCOMMAND ?ARG ...?",
		Subcommands: []*Command{
			{Name: "hold", Help: "Hold current response open for streaming", Usage: "connection hold ?-as NAME?"},
			{Name: "close", Help: "Close a held connection", Usage: "connection close HANDLE"},
			{Name: "info", Help: "Get connection info", Usage: "connection info HANDLE"},
			{Name: "onclose", Help: "Register a proc to call when connection closes", Usage: "connection onclose HANDLE PROC"},
		},
	}
	registry.Register(connectionCmd)
	interp.RegisterCommand("connection", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		if len(args) < 1 {
			return feather.Error("wrong # args: should be \"connection subcommand ?arg ...?\"")
		}
		subcmd := args[0].String()
		switch subcmd {
		case "hold":
			var name string
			// Check for -as NAME
			if len(args) >= 3 && args[1].String() == "-as" {
				name = args[2].String()
			}
			conn, err := state.HoldConnection(name)
			if err != nil {
				return feather.Errorf("connection hold: %v", err)
			}
			if name != "" {
				return feather.OK(name)
			}
			return feather.OK(conn.ID)

		case "close":
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"connection close handle\"")
			}
			handle := args[1].String()
			if err := state.CloseConnection(handle); err != nil {
				return feather.Errorf("connection close: %v", err)
			}
			return feather.OK("")

		case "info":
			if len(args) < 2 {
				return feather.Error("wrong # args: should be \"connection info handle\"")
			}
			handle := args[1].String()
			conn := state.GetConnection(handle)
			if conn == nil {
				return feather.Errorf("connection info: unknown connection %q", handle)
			}
			info := fmt.Sprintf("id %s method %s path %s opened %d",
				conn.ID,
				conn.Ctx.Request.Method,
				conn.Ctx.Request.URL.Path,
				conn.Opened.Unix())
			if conn.Name != "" {
				info = fmt.Sprintf("%s name %s", info, conn.Name)
			}
			return feather.OK(info)

		case "onclose":
			if len(args) < 3 {
				return feather.Error("wrong # args: should be \"connection onclose handle proc\"")
			}
			handle := args[1].String()
			proc := args[2].String()
			conn := state.GetConnection(handle)
			if conn == nil {
				return feather.Errorf("connection onclose: unknown connection %q", handle)
			}
			conn.OnClose = proc
			return feather.OK("")

		default:
			return feather.Errorf("connection: unknown subcommand %q (must be hold, close, info, onclose)", subcmd)
		}
	})

	// Connections command
	connectionsCmd := &Command{
		Name:  "connections",
		Help:  "List all open connection handles",
		Usage: "connections",
	}
	registry.Register(connectionsCmd)
	interp.RegisterCommand("connections", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		handles := state.ListConnections()
		return feather.OK(handles)
	})

	// Flush command
	flushCmd := &Command{
		Name:  "flush",
		Help:  "Flush buffered output",
		Usage: "flush ?-to HANDLE?",
	}
	registry.Register(flushCmd)
	interp.RegisterCommand("flush", func(i *feather.Interp, cmd feather.Object, args []feather.Object) feather.Result {
		var ctx *RequestContext

		// Check for -to HANDLE
		if len(args) >= 2 && args[0].String() == "-to" {
			handle := args[1].String()
			conn := state.GetConnection(handle)
			if conn == nil {
				return feather.OK("")
			}
			ctx = conn.Ctx
		} else {
			ctx = state.GetRequestContext()
			if ctx == nil {
				return feather.Error("flush: not in request context")
			}
		}

		if flusher, ok := ctx.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return feather.OK("")
	})
}

func parseTemplateData(args []feather.Object) (map[string]any, error) {
	data := make(map[string]any)

	// Single dict argument
	if len(args) == 1 {
		dict, err := args[0].Dict()
		if err == nil {
			for k, v := range dict {
				data[k] = v.String()
			}
			return data, nil
		}
		// Not a dict, try as list of key-value pairs
		list, err := args[0].List()
		if err == nil && len(list)%2 == 0 {
			for i := 0; i+1 < len(list); i += 2 {
				data[list[i].String()] = list[i+1].String()
			}
			return data, nil
		}
	}

	// Key value pairs as separate arguments
	for i := 0; i+1 < len(args); i += 2 {
		data[args[i].String()] = args[i+1].String()
	}
	return data, nil
}

func createHandler(state *ServerState) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle web REPL endpoints
		if r.URL.Path == "/_repl" && r.Method == "GET" {
			serveReplPage(w, r)
			return
		}
		if r.URL.Path == "/_repl/eval" && r.Method == "POST" {
			handleReplEval(state, w, r)
			return
		}

		routes := state.GetRoutes()

		for _, route := range routes {
			if matched, params := matchRoute(route, r.Method, r.URL.Path); matched {
				ctx := &RequestContext{
					Writer:  w,
					Request: r,
					Params:  params,
					Status:  200,
				}
				state.SetRequestContext(ctx)

				_, err := state.Eval(route.Body)
				if err != nil {
					if !ctx.Written {
						http.Error(w, err.Error(), http.StatusInternalServerError)
					}
				}

				// Check if this request was held as a connection
				conn := state.findConnectionByContext(ctx)
				if conn != nil {
					// Wait for connection to be closed or client disconnect
					select {
					case <-conn.Done:
						// Explicitly closed via connection close
					case <-r.Context().Done():
						// Client disconnected
						if conn.OnClose != "" {
							handle := conn.Name
							if handle == "" {
								handle = conn.ID
							}
							state.Eval(fmt.Sprintf("%s %s", conn.OnClose, handle))
						}
						// Clean up the connection
						state.CloseConnection(conn.ID)
					}
				}

				state.SetRequestContext(nil)
				return
			}
		}

		http.NotFound(w, r)
	})
}

func handleReplEval(state *ServerState, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set up eval context for streaming puts output
	evalCtx := &EvalContext{
		Output: func(msg string) {
			writeSSE(w, "output", msg)
			flusher.Flush()
		},
	}
	state.SetEvalContext(evalCtx)
	defer state.SetEvalContext(nil)

	result, err := state.Eval(string(body))
	if err != nil {
		writeSSE(w, "error", err.Error())
	} else if result.String() != "" {
		writeSSE(w, "result", result.String())
	}
	flusher.Flush()
}

func writeSSE(w io.Writer, event, data string) {
	fmt.Fprintf(w, "event: %s\n", event)
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
}

func serveReplPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(replHTML))
}

const replHTML = `<!DOCTYPE html>
<html>
<head>
    <title>feather REPL</title>
    <style>
        * { box-sizing: border-box; }
        body { 
            font-family: ui-monospace, monospace; 
            margin: 0; padding: 1rem;
            background: #1e1e1e; color: #d4d4d4;
            height: 100vh;
            display: flex; flex-direction: column;
        }
        h1 { margin: 0 0 1rem 0; font-size: 1.2rem; color: #569cd6; }
        #output {
            flex: 1;
            overflow-y: auto;
            padding: 1rem;
            background: #252526;
            border-radius: 4px;
            margin-bottom: 1rem;
            white-space: pre-wrap;
            word-wrap: break-word;
        }
        .prompt { color: #6a9955; }
        .input-line { color: #ce9178; }
        .output-line { color: #d4d4d4; }
        .result-line { color: #4ec9b0; }
        .error-line { color: #f14c4c; }
        #input-area { display: flex; gap: 0.5rem; }
        #input {
            flex: 1;
            font-family: inherit;
            font-size: inherit;
            padding: 0.5rem;
            background: #3c3c3c;
            border: 1px solid #555;
            border-radius: 4px;
            color: #d4d4d4;
            outline: none;
            resize: vertical;
            min-height: 2.5rem;
        }
        #input:focus { border-color: #569cd6; }
        button {
            padding: 0.5rem 1rem;
            background: #0e639c;
            border: none;
            border-radius: 4px;
            color: white;
            cursor: pointer;
        }
        button:hover { background: #1177bb; }
    </style>
</head>
<body>
    <h1>feather REPL</h1>
    <div id="output"><div class="prompt">Type help to get help</div></div>
    <div id="input-area">
        <textarea id="input" placeholder="Enter TCL command... (Cmd+Enter to eval)" autofocus></textarea>
        <button onclick="evaluate()">Eval</button>
    </div>
    <script>
        const output = document.getElementById('output');
        const input = document.getElementById('input');
        const history = [];
        let historyIndex = -1;

        function appendLine(text, className) {
            const line = document.createElement('div');
            line.className = className;
            line.textContent = text;
            output.appendChild(line);
            output.scrollTop = output.scrollHeight;
        }

        async function evaluate() {
            const code = input.value.trim();
            if (!code) return;

            history.unshift(code);
            historyIndex = -1;
            
            appendLine('feather> ' + code, 'input-line');
            input.value = '';

            try {
                const response = await fetch('/_repl/eval', {
                    method: 'POST',
                    body: code,
                });

                const reader = response.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';

                while (true) {
                    const {done, value} = await reader.read();
                    if (done) break;
                    
                    buffer += decoder.decode(value, {stream: true});
                    const lines = buffer.split('\n\n');
                    buffer = lines.pop();

                    for (const chunk of lines) {
                        const eventMatch = chunk.match(/^event: (\w+)\n([\s\S]*)$/);
                        if (eventMatch) {
                            const [, event, dataLines] = eventMatch;
                            const data = dataLines.split('\n')
                                .filter(l => l.startsWith('data: '))
                                .map(l => l.slice(6))
                                .join('\n');
                            if (event === 'output') {
                                appendLine(data, 'output-line');
                            } else if (event === 'result' && data) {
                                appendLine(data, 'result-line');
                            } else if (event === 'error') {
                                appendLine('error: ' + data, 'error-line');
                            }
                        }
                    }
                }
            } catch (err) {
                appendLine('error: ' + err.message, 'error-line');
            }
        }

        input.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                evaluate();
            }
        });
    </script>
</body>
</html>`

func init() {
	_ = strconv.Atoi // ensure import is used
}
